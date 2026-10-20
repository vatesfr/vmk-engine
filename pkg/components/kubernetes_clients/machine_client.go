package kubernetes_clients

import (
	"context"
	"fmt"
	"net"
	"sort"

	"github.com/gofrs/uuid"
	"github.com/samber/lo"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/strategies"
	"capv-proxy/pkg/uuids"

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
	informerCache    cache.Cache
	namespace        string
}

var _ strategies.ForManagingClusterMachines = MachineClient{}

func NewMachineClient(kubernetesClient client.Client, informerCache cache.Cache, namespace string) MachineClient {
	return MachineClient{kubernetesClient, informerCache, namespace}
}

func (m MachineClient) GetMachinesByCluster(ctx context.Context, clusterID uuid.UUID) ([]strategies.ClusterNodeInfo, error) {
	if clusterID == uuid.Nil {
		return nil, strategies.ErrClusterIDRequired
	}
	if m.namespace == "" {
		return nil, strategies.ErrClusterNamespaceRequired
	}

	machines := &capiv1.MachineList{}
	if err := m.informerCache.List(
		ctx,
		machines,
		client.InNamespace(m.namespace),
		client.MatchingFields{uuids.ClusterUUIDIndexKey: clusterID.String()},
	); err != nil {
		return nil, fmt.Errorf("list Machines for cluster %s/%s: %w", m.namespace, clusterID, err)
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
	if err := m.informerCache.List(
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

func (m MachineClient) GetMachineByUUID(ctx context.Context, id uuid.UUID) (strategies.ClusterNodeInfo, error) {
	if m.namespace == "" {
		return strategies.ClusterNodeInfo{}, strategies.ErrClusterNamespaceRequired
	}
	list := &capiv1.MachineList{}
	if err := m.informerCache.List(ctx, list,
		client.InNamespace(m.namespace),
		client.MatchingFields{uuids.UUIDIndexKey: id.String()},
	); err != nil {
		return strategies.ClusterNodeInfo{}, fmt.Errorf("list Machines by UUID: %w", err)
	}
	if len(list.Items) == 0 {
		return strategies.ClusterNodeInfo{}, strategies.ErrNodeNotFound
	}
	return toClusterNodeInfo(&list.Items[0]), nil
}

func toClusterNodeInfo(machine *capiv1.Machine) strategies.ClusterNodeInfo {
	nodeName := ""
	if machine.Status.NodeRef.IsDefined() {
		nodeName = machine.Status.NodeRef.Name
	}
	clusterName := machine.Labels[capiv1.ClusterNameLabel]
	name := machine.Name
	if nodeName != "" {
		name = nodeName
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

	_, paused := machine.Annotations[capiv1.PausedAnnotation]
	return strategies.ClusterNodeInfo{
		ID:            uuids.NodeID(clusterName, name),
		Paused:        paused,
		Name:          machine.Name,
		NodeName:      nodeName,
		Role:          role,
		Status:        status,
		Endpoint:      machineEndpoint(machine.Status.Addresses),
		MachineID:     machineID,
		ParentCluster: machine.Spec.ClusterName,
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

// SetMachinePaused changes only the Machine pause annotation. Membership is
// checked against spec.clusterName, and the patch guards against reassignment.
func (m MachineClient) SetMachinePaused(ctx context.Context, clusterName, machineName string, paused bool) error {
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
	value, present := machine.Annotations[capiv1.PausedAnnotation]
	if (paused && present && value == "") || (!paused && !present) {
		return nil
	}
	before := machine.DeepCopy()
	if paused {
		if machine.Annotations == nil {
			machine.Annotations = make(map[string]string)
		}
		machine.Annotations[capiv1.PausedAnnotation] = ""
	} else {
		delete(machine.Annotations, capiv1.PausedAnnotation)
	}
	if err := m.kubernetesClient.Patch(ctx, machine, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return pausePatchError(err, strategies.ErrNodeNotFound)
	}
	return nil
}
