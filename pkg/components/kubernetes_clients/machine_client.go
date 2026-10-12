package kubernetes_clients

import (
	"context"
	"fmt"
	"net"
	"sort"

	"github.com/gofrs/uuid"
	"github.com/samber/lo"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/strategies"

	xok8scommon "github.com/vatesfr/xenorchestra-k8s-common"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	workerRole    = "worker"
	unknownStatus = "Unknown"
)

type MachineClient struct {
	kubernetesClient client.Client
	namespace        string
}

var _ strategies.ForManagingClusterMachines = MachineClient{}

func NewMachineClient(kubernetesClient client.Client, namespace string) MachineClient {
	return MachineClient{kubernetesClient, namespace}
}

func (m MachineClient) GetMachinesByCluster(ctx context.Context, clusterName string) ([]strategies.ClusterNodeInfo, error) {
	if clusterName == "" {
		return nil, strategies.ErrClusterNameRequired
	}
	if m.namespace == "" {
		return nil, strategies.ErrClusterNamespaceRequired
	}

	machines := &capiv1.MachineList{}
	if err := m.kubernetesClient.List(
		ctx,
		machines,
		client.InNamespace(m.namespace),
		client.MatchingLabels{capiv1.ClusterNameLabel: clusterName},
	); err != nil {
		return nil, fmt.Errorf("list Machines for cluster %s/%s: %w", m.namespace, clusterName, err)
	}

	result := lo.Map(machines.Items, func(machine capiv1.Machine, _ int) strategies.ClusterNodeInfo {
		return toClusterNodeInfo(&machine)
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (m MachineClient) GetAllMachines(ctx context.Context) ([]strategies.ClusterNodeInfo, error) {
	if m.namespace == "" {
		return nil, strategies.ErrClusterNamespaceRequired
	}

	machines := &capiv1.MachineList{}
	if err := m.kubernetesClient.List(
		ctx,
		machines,
		client.InNamespace(m.namespace),
	); err != nil {
		return nil, fmt.Errorf("list Machines in namespace %s: %w", m.namespace, err)
	}

	result := lo.Map(machines.Items, func(machine capiv1.Machine, _ int) strategies.ClusterNodeInfo {
		return toClusterNodeInfo(&machine)
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func toClusterNodeInfo(machine *capiv1.Machine) strategies.ClusterNodeInfo {
	nodeName := ""
	if machine.Status.NodeRef.IsDefined() {
		nodeName = machine.Status.NodeRef.Name
	}

	role := workerRole
	if _, isControlPlane := machine.Labels[capiv1.MachineControlPlaneLabel]; isControlPlane {
		role = "control-plane"
	}

	status := machine.Status.Phase
	if ready := apimeta.FindStatusCondition(machine.Status.Conditions, capiv1.MachineReadyCondition); ready != nil {
		switch ready.Status {
		case metav1.ConditionTrue:
			status = "Ready"
		case metav1.ConditionFalse:
			status = "NotReady"
		default:
			status = unknownStatus
		}
	}
	if status == "" {
		status = unknownStatus
	}

	var machineID *uuid.UUID
	if machine.Spec.ProviderID != "" {
		// Silently ignore errors, as the provider ID may not be XenOrchestra,
		// and we don't want to fail the whole operation because of that.
		if parsedID, err := xok8scommon.GetVMID(machine.Spec.ProviderID); err == nil {
			machineID = &parsedID
		}
	}

	return strategies.ClusterNodeInfo{
		Name:      machine.Name,
		NodeName:  nodeName,
		Role:      role,
		Status:    status,
		Endpoint:  machineEndpoint(machine.Status.Addresses),
		MachineID: machineID,
	}
}

func machineEndpoint(addresses capiv1.MachineAddresses) string {
	for _, addressType := range []capiv1.MachineAddressType{
		capiv1.MachineInternalIP,
		capiv1.MachineExternalIP,
		capiv1.MachineHostName,
	} {
		for _, address := range addresses {
			if address.Type == addressType && address.Address != "" {
				return "https://" + net.JoinHostPort(address.Address, "10250")
			}
		}
	}
	return ""
}

// DeleteMachine requests deletion by the CAPI resource name.
// Membership is checked using spec.clusterName rather than mutable labels.
func (m MachineClient) DeleteMachine(ctx context.Context, clusterName, machineName string) error {
	if clusterName == "" {
		return strategies.ErrClusterNameRequired
	}
	if machineName == "" {
		return strategies.ErrNodeNameRequired
	}
	if m.namespace == "" {
		return strategies.ErrClusterNamespaceRequired
	}

	machine := &capiv1.Machine{}
	key := client.ObjectKey{Namespace: m.namespace, Name: machineName}
	if err := m.kubernetesClient.Get(ctx, key, machine); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: %w", strategies.ErrNodeNotFound, err)
		}
		return fmt.Errorf("get Machine %s: %w", key, err)
	}
	if machine.Spec.ClusterName != clusterName {
		return strategies.ErrNodeNotFound
	}

	// Protect against replacement or reassignment between the read and delete.
	if err := m.kubernetesClient.Delete(ctx, machine, client.Preconditions{
		UID: &machine.UID, ResourceVersion: &machine.ResourceVersion,
	}); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: %w", strategies.ErrNodeNotFound, err)
		}
		if apierrors.IsConflict(err) {
			return fmt.Errorf("%w: %w", strategies.ErrNodeDeletionConflict, err)
		}
		return fmt.Errorf("delete Machine %s: %w", key, err)
	}
	return nil
}
