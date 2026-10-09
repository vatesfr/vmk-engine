package kubernetes_clients

import (
	"context"
	"fmt"
	"net"
	"sort"

	"github.com/gofrs/uuid"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/strategies"

	xok8scommon "github.com/vatesfr/xenorchestra-k8s-common"

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

var _ strategies.ForGettingClusterMachines = MachineClient{}

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

	result := make([]strategies.ClusterNodeInfo, 0, len(machines.Items))
	for i := range machines.Items {
		result = append(result, toClusterNodeInfo(&machines.Items[i]))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func toClusterNodeInfo(machine *capiv1.Machine) strategies.ClusterNodeInfo {
	name := machine.Name
	if machine.Status.NodeRef.IsDefined() {
		name = machine.Status.NodeRef.Name
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
		Name:      name,
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
