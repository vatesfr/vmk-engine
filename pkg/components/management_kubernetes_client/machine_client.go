package management_kubernetes_client

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

	xok8scommon "github.com/vatesfr/xenorchestra-k8s-common"

	uuids "github.com/vatesfr/vmk-engine/pkg/helpers/uuids"
	"github.com/vatesfr/vmk-engine/pkg/strategies"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	workerRole     = "worker"
	unknownStatus  = "Unknown"
	unknownVersion = "unknown"
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
	if id == uuid.Nil {
		return strategies.ClusterNodeInfo{}, strategies.ErrNodeIDRequired
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
	if len(list.Items) > 1 {
		return strategies.ClusterNodeInfo{}, fmt.Errorf("%w: %d Machines found for UUID %s", strategies.ErrNodeAmbiguous, len(list.Items), id)
	}
	return toClusterNodeInfo(&list.Items[0]), nil
}

func (m MachineClient) validateMachineMutationInput(clusterID, machineID uuid.UUID) error {
	if clusterID == uuid.Nil {
		return strategies.ErrClusterIDRequired
	}
	if machineID == uuid.Nil {
		return strategies.ErrNodeIDRequired
	}
	if m.namespace == "" {
		return strategies.ErrClusterNamespaceRequired
	}
	return nil
}

func (m MachineClient) getMachineByUUIDInCluster(ctx context.Context, clusterID, machineID uuid.UUID) (*capiv1.Machine, error) {
	machines := &capiv1.MachineList{}
	if err := m.informerCache.List(ctx, machines,
		client.InNamespace(m.namespace),
		client.MatchingFields{
			uuids.UUIDIndexKey:        machineID.String(),
			uuids.ClusterUUIDIndexKey: clusterID.String(),
		},
	); err != nil {
		return nil, fmt.Errorf("list Machines by UUID %s in cluster %s: %w", machineID, clusterID, err)
	}
	if len(machines.Items) == 0 {
		return nil, strategies.ErrNodeNotFound
	}
	if len(machines.Items) > 1 {
		return nil, fmt.Errorf("%w: %d Machines found for UUID %s in cluster %s", strategies.ErrNodeAmbiguous, len(machines.Items), machineID, clusterID)
	}
	return &machines.Items[0], nil
}

func toClusterNodeInfo(machine *capiv1.Machine) strategies.ClusterNodeInfo {
	nodeName := ""
	if machine.Status.NodeRef.IsDefined() {
		nodeName = machine.Status.NodeRef.Name
	}
	clusterName := machine.Spec.ClusterName

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

	var vmID *uuid.UUID
	if machine.Spec.ProviderID != "" {
		// Silently ignore errors, as the provider ID may not be XenOrchestra,
		// and we don't want to fail the whole operation because of that.
		if parsedID, err := xok8scommon.GetVMID(machine.Spec.ProviderID); err == nil {
			vmID = &parsedID
		}
	}

	_, paused := machine.Annotations[capiv1.PausedAnnotation]
	return strategies.ClusterNodeInfo{
		IsDeleting:    !machine.DeletionTimestamp.IsZero(),
		ID:            uuids.NodeID(clusterName, machine.Name),
		Paused:        paused,
		Name:          machine.Name,
		NodeName:      nodeName,
		Role:          role,
		Status:        status,
		IPs:           machineIPs(machine.Status.Addresses),
		VMID:          vmID,
		ParentCluster: uuids.ClusterID(clusterName),
		Version:       machineVersion(machine),
	}
}

// machineVersion returns the desired Machine version, falling back to the
// kubelet version reported by the node. Some control plane providers do not
// set spec.version on their Machines, so the version may be unknown.
func machineVersion(machine *capiv1.Machine) string {
	if machine.Spec.Version != "" {
		return machine.Spec.Version
	}
	if machine.Status.NodeInfo != nil && machine.Status.NodeInfo.KubeletVersion != "" {
		return machine.Status.NodeInfo.KubeletVersion
	}
	return unknownVersion
}

func machineIPs(addresses capiv1.MachineAddresses) []string {
	var ips []string
	seen := make(map[string]bool)
	for _, address := range addresses {
		if address.Type != capiv1.MachineInternalIP && address.Type != capiv1.MachineExternalIP {
			continue
		}
		if net.ParseIP(address.Address) == nil || seen[address.Address] {
			continue
		}
		ips = append(ips, address.Address)
		seen[address.Address] = true
	}
	return ips
}

// DeleteMachine requests deletion by the node UUID.
// Membership is checked using spec.clusterName rather than mutable labels.
func (m MachineClient) DeleteMachine(ctx context.Context, clusterID, machineID uuid.UUID) error {
	if err := m.validateMachineMutationInput(clusterID, machineID); err != nil {
		return err
	}
	machine, err := m.getMachineByUUIDInCluster(ctx, clusterID, machineID)
	if err != nil {
		return err
	}
	clusters := NewClusterClient(m.kubernetesClient, m.informerCache, m.namespace)
	return clusters.withClusterMutation(ctx, machine.Spec.ClusterName, func(ctx context.Context) error { return m.deleteMachine(ctx, clusterID, machineID) })
}

func (m MachineClient) deleteMachine(ctx context.Context, clusterID, machineID uuid.UUID) error {
	if err := m.validateMachineMutationInput(clusterID, machineID); err != nil {
		return err
	}

	machine, err := m.getMachineByUUIDInCluster(ctx, clusterID, machineID)
	if err != nil {
		return err
	}

	key := client.ObjectKeyFromObject(machine)
	if err := m.kubernetesClient.Get(ctx, key, machine); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: %w", strategies.ErrNodeNotFound, err)
		}
		return fmt.Errorf("get Machine %s: %w", key, err)
	}
	node := toClusterNodeInfo(machine)
	if node.ID != machineID || node.ParentCluster != clusterID {
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
		return fmt.Errorf("delete Machine %s: %w", key, mutationWriteError(err))
	}
	return nil
}

// SetMachinePaused changes only the Machine pause annotation. Membership is
// checked against spec.clusterName, and the patch guards against reassignment.
func (m MachineClient) SetMachinePaused(ctx context.Context, clusterID, machineID uuid.UUID, paused bool) error {
	if err := m.validateMachineMutationInput(clusterID, machineID); err != nil {
		return err
	}
	machine, err := m.getMachineByUUIDInCluster(ctx, clusterID, machineID)
	if err != nil {
		return err
	}
	clusters := NewClusterClient(m.kubernetesClient, m.informerCache, m.namespace)
	return clusters.withClusterMutation(ctx, machine.Spec.ClusterName, func(ctx context.Context) error { return m.setMachinePaused(ctx, clusterID, machineID, paused) })
}

func (m MachineClient) setMachinePaused(ctx context.Context, clusterID, machineID uuid.UUID, paused bool) error {
	if err := m.validateMachineMutationInput(clusterID, machineID); err != nil {
		return err
	}
	machine, err := m.getMachineByUUIDInCluster(ctx, clusterID, machineID)
	if err != nil {
		return err
	}

	key := client.ObjectKeyFromObject(machine)
	if err := m.kubernetesClient.Get(ctx, key, machine); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: %w", strategies.ErrNodeNotFound, err)
		}
		return fmt.Errorf("get Machine %s: %w", key, err)
	}
	node := toClusterNodeInfo(machine)
	if node.ID != machineID || node.ParentCluster != clusterID {
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

// GetNodeName resolves a Machine by UUID and rechecks its membership with a fresh read.
func (m MachineClient) GetNodeName(ctx context.Context, clusterID, machineID uuid.UUID) (string, error) {
	if err := m.validateMachineMutationInput(clusterID, machineID); err != nil {
		return "", err
	}
	machine, err := m.getMachineByUUIDInCluster(ctx, clusterID, machineID)
	if err != nil {
		return "", err
	}
	if err := m.kubernetesClient.Get(ctx, client.ObjectKeyFromObject(machine), machine); err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("%w: %w", strategies.ErrNodeNotFound, err)
		}
		return "", err
	}
	node := toClusterNodeInfo(machine)
	if node.ID != machineID || node.ParentCluster != clusterID {
		return "", strategies.ErrNodeNotFound
	}
	if node.NodeName == "" {
		return "", strategies.ErrNodeNotRegistered
	}
	return node.NodeName, nil
}
