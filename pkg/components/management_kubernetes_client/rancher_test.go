package management_kubernetes_client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vatesfr/vmk-engine/pkg/components/management_kubernetes_client/flavor"
	uuids "github.com/vatesfr/vmk-engine/pkg/helpers/uuids"
	"github.com/vatesfr/vmk-engine/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"
)

const (
	rancherTestCluster   = "xo-lab"
	rancherTestVersion   = "v1.36.6+rke2r1"
	rancherRoleEnabled   = "true"
	rancherQuantityKey   = "quantity"
	rancherWorkerRoleKey = "workerRole"
)

func rancherTestPool(name string, controlPlane, etcd, worker bool, quantity int64) map[string]any {
	return map[string]any{
		nameKey: name, "controlPlaneRole": controlPlane, "etcdRole": etcd, rancherWorkerRoleKey: worker, rancherQuantityKey: quantity,
	}
}

// rancherObjects mirrors a Rancher v2 provisioned cluster: one CAPI Cluster
// controlled by a provisioning.cattle.io Cluster, with pools as MachineDeployments.
func rancherObjects(globalConfig map[string]any, pools ...any) (*capiv1.Cluster, *unstructured.Unstructured) {
	rancherCluster := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"kubernetesVersion": rancherTestVersion,
			"rkeConfig":         map[string]any{"machineGlobalConfig": globalConfig, "machinePools": pools},
		},
	}}
	rancherCluster.SetGroupVersionKind(flavor.RancherClusterGVK)
	rancherCluster.SetNamespace(testNamespace)
	rancherCluster.SetName(rancherTestCluster)
	rancherCluster.SetUID("rancher-uid")

	cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name: rancherTestCluster, Namespace: testNamespace, UID: "capi-uid",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: "provisioning.cattle.io/v1", Kind: "Cluster", Name: rancherTestCluster,
			UID: "rancher-uid", Controller: ptr.To(true),
		}},
	}}
	cluster.Spec.ControlPlaneRef = capiv1.ContractVersionedObjectReference{
		APIGroup: flavor.RancherControlPlaneGroup, Kind: flavor.RancherControlPlaneKind, Name: rancherTestCluster,
	}
	// Rancher's Cluster status counts the control plane pool as workers.
	cluster.Status.Workers = &capiv1.WorkersStatus{Replicas: ptr.To(int32(2)), DesiredReplicas: ptr.To(int32(2))}
	return cluster, rancherCluster
}

func rancherMachine(name string, controlPlane, ready bool) *capiv1.Machine {
	labels := map[string]string{capiv1.ClusterNameLabel: rancherTestCluster, "rke.cattle.io/worker-role": rancherRoleEnabled}
	if controlPlane {
		labels["rke.cattle.io/control-plane-role"] = rancherRoleEnabled
		labels[capiv1.MachineControlPlaneLabel] = "true"
	}
	machine := &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace, Labels: labels}}
	machine.Spec.ClusterName = rancherTestCluster
	machine.Status.NodeInfo = &corev1.NodeSystemInfo{KubeletVersion: rancherTestVersion}
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	for _, kind := range []string{capiv1.MachineReadyCondition, capiv1.MachineAvailableCondition, capiv1.MachineUpToDateCondition} {
		machine.Status.Conditions = append(machine.Status.Conditions, metav1.Condition{Type: kind, Status: status, Reason: "Test"})
	}
	return machine
}

func TestRancherClusterInfo(t *testing.T) {
	t.Run("defaults and roles", func(t *testing.T) {
		cluster, rancherCluster := rancherObjects(nil, rancherTestPool("ctrl", true, true, true, 2))
		k8s := newFakeClient(t, cluster, rancherCluster,
			rancherMachine("ctrl-a", true, true), rancherMachine("ctrl-b", true, false))
		info, err := NewClusterClient(k8s, clientAsCache{k8s}, testNamespace).GetByUUID(t.Context(), uuids.ClusterID(rancherTestCluster))

		require.NoError(t, err)
		assert.Equal(t, rancherTestVersion, info.Version)
		assert.Equal(t, flavor.RancherControlPlaneKind, info.ControlPlaneKind)
		assert.Equal(t, []string{"10.42.0.0/16"}, info.PodCIDRBlocks)
		assert.Equal(t, []string{"10.43.0.0/16"}, info.ServiceCIDRBlocks)
		assert.Equal(t, "cluster.local", info.ServiceDomain)
		assert.Equal(t, strategies.NodeTypeStatus{
			DesiredReplicas: 2, Replicas: 2, ReadyReplicas: 1, AvailableReplicas: 1, UpToDateReplicas: 1,
			Versions: []strategies.VersionNodeStatus{{Replicas: 2, Version: rancherTestVersion}},
		}, info.ControlPlanesStatus)
		assert.Equal(t, strategies.NodeTypeStatus{}, info.WorkersStatus)
	})

	t.Run("network overrides and dedicated workers", func(t *testing.T) {
		cluster, rancherCluster := rancherObjects(map[string]any{
			"cluster-cidr": "10.1.0.0/16, fd00::/56", "service-cidr": "10.2.0.0/16", "cluster-domain": "lab.local",
		}, rancherTestPool("ctrl", true, true, false, 1), rancherTestPool("work", false, false, true, 3))
		k8s := newFakeClient(t, cluster, rancherCluster, rancherMachine("ctrl-a", true, true), rancherMachine("work-a", false, true))
		infos, err := NewClusterClient(k8s, clientAsCache{k8s}, testNamespace).List(t.Context())

		require.NoError(t, err)
		require.Len(t, infos, 1)
		info := infos[0]
		assert.Equal(t, []string{"10.1.0.0/16", "fd00::/56"}, info.PodCIDRBlocks)
		assert.Equal(t, []string{"10.2.0.0/16"}, info.ServiceCIDRBlocks)
		assert.Equal(t, "lab.local", info.ServiceDomain)
		assert.Equal(t, 1, info.ControlPlanesStatus.Replicas)
		assert.Equal(t, 1, info.ControlPlanesStatus.DesiredReplicas)
		assert.Equal(t, 1, info.WorkersStatus.Replicas)
		assert.Equal(t, 3, info.WorkersStatus.DesiredReplicas)
	})

	t.Run("missing Rancher cluster keeps the CAPI data", func(t *testing.T) {
		cluster, _ := rancherObjects(nil)
		k8s := newFakeClient(t, cluster)
		info, err := NewClusterClient(k8s, clientAsCache{k8s}, testNamespace).GetByUUID(t.Context(), uuids.ClusterID(rancherTestCluster))

		require.NoError(t, err)
		assert.Empty(t, info.Version)
		assert.Equal(t, 2, info.WorkersStatus.Replicas)
	})
}

func rancherPoolQuantities(t *testing.T, k8s client.Client) []int64 {
	t.Helper()
	rancherCluster := &unstructured.Unstructured{}
	rancherCluster.SetGroupVersionKind(flavor.RancherClusterGVK)
	require.NoError(t, k8s.Get(t.Context(), client.ObjectKey{Namespace: testNamespace, Name: rancherTestCluster}, rancherCluster))
	pools, _, err := unstructured.NestedSlice(rancherCluster.Object, "spec", "rkeConfig", "machinePools")
	require.NoError(t, err)
	var result []int64
	for _, pool := range pools {
		result = append(result, pool.(map[string]any)[rancherQuantityKey].(int64))
	}
	return result
}

func TestRancherScaling(t *testing.T) {
	id := uuids.ClusterID(rancherTestCluster)

	t.Run("control plane scales the control plane pool", func(t *testing.T) {
		cluster, rancherCluster := rancherObjects(nil, rancherTestPool("ctrl", true, true, true, 1), rancherTestPool("work", false, false, true, 2))
		k8s := newFakeClient(t, cluster, rancherCluster)
		service := NewClusterClient(k8s, clientAsCache{k8s}, testNamespace)

		require.NoError(t, service.SetControlPlaneReplicas(t.Context(), id, 3))
		assert.Equal(t, []int64{3, 2}, rancherPoolQuantities(t, k8s))
		require.ErrorIs(t, service.SetControlPlaneReplicas(t.Context(), id, 2), strategies.ErrInvalidControlPlaneReplicas)
	})

	t.Run("workers scale only a worker-only pool", func(t *testing.T) {
		cluster, rancherCluster := rancherObjects(nil, rancherTestPool("ctrl", true, true, true, 1), rancherTestPool("work", false, false, true, 2))
		k8s := newFakeClient(t, cluster, rancherCluster)

		require.NoError(t, NewClusterClient(k8s, clientAsCache{k8s}, testNamespace).SetWorkerReplicas(t.Context(), id, 4))
		assert.Equal(t, []int64{1, 4}, rancherPoolQuantities(t, k8s))
	})

	conflicts := map[string][]any{
		"no worker-only pool":    {rancherTestPool("ctrl", true, true, true, 2)},
		"several worker pools":   {rancherTestPool("a", false, false, true, 1), rancherTestPool("b", false, false, true, 1)},
		"autoscaled worker pool": {map[string]any{"name": "w", rancherWorkerRoleKey: true, rancherQuantityKey: int64(1), "autoscalingMinSize": int64(1)}},
		"paused worker pool":     {map[string]any{"name": "w", rancherWorkerRoleKey: true, rancherQuantityKey: int64(1), "paused": true}},
	}
	for name, pools := range conflicts {
		t.Run(name, func(t *testing.T) {
			cluster, rancherCluster := rancherObjects(nil, pools...)
			k8s := newFakeClient(t, cluster, rancherCluster)
			err := NewClusterClient(k8s, clientAsCache{k8s}, testNamespace).SetWorkerReplicas(t.Context(), id, 3)
			require.ErrorIs(t, err, strategies.ErrClusterScalingConflict)
		})
	}

	t.Run("replaced Rancher owner is refused", func(t *testing.T) {
		cluster, rancherCluster := rancherObjects(nil, rancherTestPool("ctrl", true, true, true, 1))
		rancherCluster.SetUID("other-uid")
		k8s := newFakeClient(t, cluster, rancherCluster)
		require.Error(t, NewClusterClient(k8s, clientAsCache{k8s}, testNamespace).SetControlPlaneReplicas(t.Context(), id, 3))
		assert.Equal(t, []int64{1}, rancherPoolQuantities(t, k8s))
	})
}

func TestRancherDeleteAndUpgrade(t *testing.T) {
	cluster, rancherCluster := rancherObjects(nil, rancherTestPool("ctrl", true, true, true, 1))
	k8s := newFakeClient(t, cluster, rancherCluster)
	service := NewClusterClient(k8s, clientAsCache{k8s}, testNamespace)

	_, err := service.flavor(cluster).UpgradeControlPlane(t.Context(), cluster)
	require.ErrorIs(t, err, strategies.ErrClusterUpgradeConflict)

	require.NoError(t, service.Delete(t.Context(), uuids.ClusterID(rancherTestCluster)))
	err = k8s.Get(t.Context(), client.ObjectKeyFromObject(rancherCluster), rancherCluster)
	assert.True(t, apierrors.IsNotFound(err), "the Rancher cluster must be deleted")
	// Rancher deletes the CAPI Cluster it owns; the engine must not.
	require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(cluster), cluster))
}
