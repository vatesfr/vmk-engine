package flavor

import (
	"context"
	"fmt"
	"strings"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	uuids "github.com/vatesfr/vmk-engine/pkg/helpers/uuids"
	"github.com/vatesfr/vmk-engine/pkg/strategies"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
)

// Rancher (v2 provisioning) builds a CAPI Cluster whose control plane is an
// RKEControlPlane and whose machine pools, control plane included, are all
// MachineDeployments. Rancher re-applies those CAPI objects from its
// provisioning.cattle.io Cluster, so that object is the only one read for
// configuration and the only one written.
const (
	RancherControlPlaneGroup = "rke.cattle.io"
	RancherControlPlaneKind  = "RKEControlPlane"
	rancherProvisioningGroup = "provisioning.cattle.io"
	rancherClusterKind       = "Cluster"

	rancherControlPlaneRoleLabel = "rke.cattle.io/control-plane-role"
	rancherWorkerRoleLabel       = "rke.cattle.io/worker-role"
	rancherRoleEnabled           = "true"
	rancherQuantityKey           = "quantity"
	rancherControlPlaneRoleKey   = "controlPlaneRole"
	rancherEtcdRoleKey           = "etcdRole"
	rancherWorkerRoleKey         = "workerRole"

	// RKE2 and K3s defaults, used when machineGlobalConfig does not override them.
	rancherDefaultPodCIDR       = "10.42.0.0/16"
	rancherDefaultServiceCIDR   = "10.43.0.0/16"
	rancherDefaultServiceDomain = "cluster.local"
)

var RancherClusterGVK = schema.GroupVersionKind{Group: rancherProvisioningGroup, Version: "v1", Kind: rancherClusterKind}

// rancherFlavor serves clusters provisioned by Rancher.
type rancherFlavor struct {
	Host
}

func (c rancherFlavor) AddInfo(ctx context.Context, cluster *capiv1.Cluster, info *strategies.ClusterInfo) error {
	machines := &capiv1.MachineList{}
	if err := c.Cache().List(ctx, machines, client.InNamespace(c.Namespace()),
		client.MatchingFields{uuids.ClusterUUIDIndexKey: info.ID.String()}); err != nil {
		return fmt.Errorf("list Machines: %w", err)
	}
	rancherCluster, err := c.getRancherCluster(ctx, c.Cache(), cluster)
	if err != nil {
		return err
	}
	pools, err := rancherMachinePools(rancherCluster)
	if err != nil {
		return err
	}

	info.Version, _, _ = unstructured.NestedString(rancherCluster.Object, specKey, "kubernetesVersion")
	config, _, _ := unstructured.NestedMap(rancherCluster.Object, specKey, "rkeConfig", "machineGlobalConfig")
	info.PodCIDRBlocks = rancherCIDRs(config["cluster-cidr"], rancherDefaultPodCIDR)
	info.ServiceCIDRBlocks = rancherCIDRs(config["service-cidr"], rancherDefaultServiceCIDR)
	info.ServiceDomain = rancherDefaultServiceDomain
	if domain, ok := config["cluster-domain"].(string); ok && domain != "" {
		info.ServiceDomain = domain
	}

	// Cluster.status counts every MachineDeployment as workers, control plane
	// pools included, so both statuses are rebuilt from the Machine roles.
	var controlPlanes, workers []capiv1.Machine
	for _, machine := range machines.Items {
		switch {
		case machine.Labels[rancherControlPlaneRoleLabel] == rancherRoleEnabled:
			controlPlanes = append(controlPlanes, machine)
		case machine.Labels[rancherWorkerRoleLabel] == rancherRoleEnabled:
			workers = append(workers, machine)
		}
	}
	info.ControlPlanesStatus = c.machinesStatus(controlPlanes, desiredPoolQuantity(pools, true))
	info.WorkersStatus = c.machinesStatus(workers, desiredPoolQuantity(pools, false))
	return nil
}

// rancherCIDRs reads an RKE2 CIDR setting: a comma-separated string, one
// entry per IP family.
func rancherCIDRs(value any, fallback string) []string {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return []string{fallback}
	}
	var result []string
	for cidr := range strings.SplitSeq(text, ",") {
		if cidr = strings.TrimSpace(cidr); cidr != "" {
			result = append(result, cidr)
		}
	}
	return result
}

func (c rancherFlavor) machinesStatus(machines []capiv1.Machine, desired int) strategies.NodeTypeStatus {
	status := strategies.NodeTypeStatus{DesiredReplicas: desired, Replicas: len(machines)}
	versions := map[string]int{}
	var order []string
	for i := range machines {
		conditions := machines[i].Status.Conditions
		if apimeta.IsStatusConditionTrue(conditions, capiv1.MachineAvailableCondition) {
			status.AvailableReplicas++
		}
		if apimeta.IsStatusConditionTrue(conditions, capiv1.MachineReadyCondition) {
			status.ReadyReplicas++
		}
		if apimeta.IsStatusConditionTrue(conditions, capiv1.MachineUpToDateCondition) {
			status.UpToDateReplicas++
		}
		version := c.MachineVersion(&machines[i])
		if versions[version] == 0 {
			order = append(order, version)
		}
		versions[version]++
	}
	for _, version := range order {
		status.Versions = append(status.Versions, strategies.VersionNodeStatus{Replicas: versions[version], Version: version})
	}
	return status
}

// rancherPool is the subset of a Rancher machine pool the engine needs.
type rancherPool struct {
	name         string
	controlPlane bool
	etcd         bool
	worker       bool
	quantity     int64
}

// isTarget tells whether scaling the control plane (or the workers) resizes
// this pool. Workers are only the pools without the control plane role, so a
// worker request never resizes a control plane.
func (p rancherPool) isTarget(controlPlane bool) bool {
	if controlPlane {
		return p.controlPlane
	}
	return p.worker && !p.controlPlane
}

func rancherMachinePools(rancherCluster *unstructured.Unstructured) ([]rancherPool, error) {
	raw, _, err := unstructured.NestedSlice(rancherCluster.Object, specKey, "rkeConfig", "machinePools")
	if err != nil {
		return nil, fmt.Errorf("read Rancher machine pools: %w", err)
	}
	pools := make([]rancherPool, 0, len(raw))
	for _, item := range raw {
		pool, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("read Rancher machine pools: unexpected entry %T", item)
		}
		flag := func(key string) bool { value, _ := pool[key].(bool); return value }
		name, _ := pool[nameKey].(string)
		// Rancher copies quantity to MachineDeployment.spec.replicas, which
		// CAPI defaults to 1 when unset.
		quantity, found, err := unstructured.NestedInt64(pool, rancherQuantityKey)
		if err != nil {
			return nil, fmt.Errorf("read Rancher machine pool %q quantity: %w", name, err)
		}
		if !found {
			quantity = 1
		}
		pools = append(pools, rancherPool{
			name: name, controlPlane: flag(rancherControlPlaneRoleKey), etcd: flag(rancherEtcdRoleKey), worker: flag(rancherWorkerRoleKey), quantity: quantity,
		})
	}
	return pools, nil
}

func desiredPoolQuantity(pools []rancherPool, controlPlane bool) int {
	total := 0
	for _, pool := range pools {
		if pool.isTarget(controlPlane) {
			total += int(pool.quantity)
		}
	}
	return total
}

// getRancherCluster returns the provisioning.cattle.io Cluster controlling the
// CAPI Cluster, read through reader (the informer cache or the API client).
func (c rancherFlavor) getRancherCluster(ctx context.Context, reader client.Reader, cluster *capiv1.Cluster) (*unstructured.Unstructured, error) {
	owner := rancherOwner(cluster)
	if owner == nil {
		return nil, fmt.Errorf("cluster %s has no controlling %s", cluster.Name, RancherClusterGVK.GroupKind())
	}
	rancherCluster := &unstructured.Unstructured{}
	rancherCluster.SetGroupVersionKind(RancherClusterGVK)
	if err := reader.Get(ctx, client.ObjectKey{Namespace: c.Namespace(), Name: owner.Name}, rancherCluster); err != nil {
		return nil, fmt.Errorf("get Rancher cluster %s: %w", owner.Name, err)
	}
	if rancherCluster.GetUID() != owner.UID {
		return nil, fmt.Errorf("get Rancher cluster %s: owner was replaced", owner.Name)
	}
	return rancherCluster, nil
}

func rancherOwner(cluster *capiv1.Cluster) *metav1.OwnerReference {
	for i, owner := range cluster.OwnerReferences {
		gv, err := schema.ParseGroupVersion(owner.APIVersion)
		if err == nil && gv.Group == rancherProvisioningGroup && owner.Kind == rancherClusterKind && ptr.Deref(owner.Controller, false) {
			return &cluster.OwnerReferences[i]
		}
	}
	return nil
}

func (c rancherFlavor) SetControlPlaneReplicas(ctx context.Context, cluster *capiv1.Cluster, replicas int32) error {
	return c.setPoolQuantity(ctx, cluster, true, replicas)
}

func (c rancherFlavor) SetWorkerReplicas(ctx context.Context, cluster *capiv1.Cluster, replicas int32) error {
	return c.setPoolQuantity(ctx, cluster, false, replicas)
}

// setPoolQuantity scales the single Rancher machine pool targeted by a
// control plane (or worker) request. Rancher then resizes the matching
// MachineDeployment; patching that MachineDeployment directly would be reverted.
func (c rancherFlavor) setPoolQuantity(ctx context.Context, cluster *capiv1.Cluster, controlPlane bool, replicas int32) error {
	rancherCluster, err := c.getRancherCluster(ctx, c.Client(), cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return scalingConflict("Rancher cluster is missing")
		}
		return err
	}
	if !rancherCluster.GetDeletionTimestamp().IsZero() {
		return scalingConflict("Rancher cluster is being deleted")
	}
	pools, err := rancherMachinePools(rancherCluster)
	if err != nil {
		return err
	}
	target := -1
	for i, pool := range pools {
		if pool.isTarget(controlPlane) {
			if target >= 0 {
				return scalingConflict("scaling requires exactly one matching Rancher machine pool")
			}
			target = i
		}
	}
	if target < 0 {
		if controlPlane {
			return scalingConflict("no Rancher machine pool has the control plane role")
		}
		return scalingConflict("no Rancher machine pool has only the worker role")
	}
	pool := pools[target]
	if pool.etcd && replicas%2 == 0 {
		return strategies.ErrInvalidControlPlaneReplicas
	}

	raw, _, _ := unstructured.NestedSlice(rancherCluster.Object, specKey, "rkeConfig", "machinePools")
	entry := raw[target].(map[string]any)
	if paused, _ := entry["paused"].(bool); paused {
		return scalingConflict("Rancher machine pool is paused")
	}
	for _, key := range []string{"autoscalingMinSize", "autoscalingMaxSize"} {
		if _, found := entry[key]; found {
			return scalingConflict("Rancher machine pool is managed by an autoscaler")
		}
	}
	if pool.quantity == int64(replicas) {
		return nil
	}
	before := rancherCluster.DeepCopy()
	entry[rancherQuantityKey] = int64(replicas)
	if err := unstructured.SetNestedSlice(rancherCluster.Object, raw, specKey, "rkeConfig", "machinePools"); err != nil {
		return err
	}
	return c.PatchReplicas(ctx, rancherCluster, before)
}

// delete deletes the provisioning.cattle.io Cluster: Rancher would recreate
// a CAPI Cluster deleted on its own.
func (c rancherFlavor) Delete(ctx context.Context, cluster *capiv1.Cluster) error {
	rancherCluster, err := c.getRancherCluster(ctx, c.Client(), cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: %w", strategies.ErrClusterNotFound, err)
		}
		return err
	}
	uid, version := rancherCluster.GetUID(), rancherCluster.GetResourceVersion()
	if err := c.Client().Delete(ctx, rancherCluster, client.Preconditions{UID: &uid, ResourceVersion: &version}); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: %w", strategies.ErrClusterNotFound, err)
		}
		if apierrors.IsConflict(err) {
			return upgradeConflict("cluster changed during deletion; read its lock state before retrying")
		}
		return fmt.Errorf("delete Rancher cluster %s/%s: %w", c.Namespace(), rancherCluster.GetName(), c.MutationWriteError(err))
	}
	return nil
}

// upgradeControlPlane is refused: Rancher upgrades from
// spec.kubernetesVersion with its own RKE2 release catalog.
func (rancherFlavor) UpgradeControlPlane(_ context.Context, cluster *capiv1.Cluster) (*unstructured.Unstructured, error) {
	return nil, unsupportedUpgradeConflict(cluster)
}
