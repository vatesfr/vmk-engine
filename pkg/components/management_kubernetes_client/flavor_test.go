package management_kubernetes_client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vatesfr/vmk-engine/pkg/components/management_kubernetes_client/flavor"
	"github.com/vatesfr/vmk-engine/pkg/strategies"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const unknownControlPlaneKind = "UnknownControlPlane"

// vatesKubeOSObjects turns the kubeadm scaling fixture into a Vates Kube OS
// cluster: same Cluster and workers, a VatesControlPlane v1alpha1.
func vatesKubeOSObjects() (*capiv1.Cluster, *capiv1.MachineDeployment, *unstructured.Unstructured) {
	cluster, md, cp := scalingObjects()
	cluster.Spec.ControlPlaneRef.Kind = flavor.VatesControlPlaneKind
	cp.SetGroupVersionKind(flavor.VatesControlPlaneGVK)
	cp.Object[specKey] = map[string]any{replicasKey: int64(1), versionKey: "v1.36.2"}
	return cluster, md, cp
}

func TestContractFlavorVatesKubeOS(t *testing.T) {
	cluster, md, cp := vatesKubeOSObjects()
	k8s := newFakeClient(t, cluster, md, cp)
	service := NewClusterClient(k8s, clientAsCache{k8s}, testNamespace)

	require.NoError(t, service.SetControlPlaneReplicas(t.Context(), testClusterUUID, 3))
	require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(cp), cp))
	replicas, _, err := unstructured.NestedInt64(cp.Object, specKey, replicasKey)
	require.NoError(t, err)
	assert.Equal(t, int64(3), replicas)
	require.ErrorIs(t, service.SetControlPlaneReplicas(t.Context(), testClusterUUID, 2), strategies.ErrInvalidControlPlaneReplicas)

	require.NoError(t, service.SetWorkerReplicas(t.Context(), testClusterUUID, 4))
	require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(md), md))
	assert.Equal(t, int32(4), *md.Spec.Replicas)

	_, err = service.flavor(cluster).UpgradeControlPlane(t.Context(), cluster)
	require.ErrorIs(t, err, strategies.ErrClusterUpgradeConflict)
}

func TestUnsupportedFlavor(t *testing.T) {
	cluster, md, cp := scalingObjects()
	cluster.Spec.ControlPlaneRef.Kind = unknownControlPlaneKind
	k8s := newFakeClient(t, cluster, md, cp)
	service := NewClusterClient(k8s, clientAsCache{k8s}, testNamespace)

	// Its owner may re-render the MachineDeployment, as Rancher does.
	require.ErrorIs(t, service.SetWorkerReplicas(t.Context(), testClusterUUID, 3), strategies.ErrClusterScalingConflict)
	require.NoError(t, k8s.Get(t.Context(), client.ObjectKeyFromObject(md), md))
	assert.Equal(t, int32(1), *md.Spec.Replicas)

	info, err := service.GetByUUID(t.Context(), testClusterUUID)
	require.NoError(t, err)
	assert.Equal(t, strategies.ClusterCapabilities{}, info.Capabilities)

	require.NoError(t, service.Delete(t.Context(), testClusterUUID))
	assert.True(t, apierrors.IsNotFound(k8s.Get(t.Context(), client.ObjectKeyFromObject(cluster), cluster)))
}

func TestFlavorCapabilities(t *testing.T) {
	cases := map[string]struct {
		group, kind string
		want        strategies.ClusterCapabilities
	}{
		"kubeadm":        {cpGroup, controlPlaneKind, strategies.ClusterCapabilities{ScaleControlPlane: true, ScaleWorkers: true, Upgrade: true}},
		"vates kube os":  {cpGroup, flavor.VatesControlPlaneKind, strategies.ClusterCapabilities{ScaleControlPlane: true, ScaleWorkers: true}},
		"rancher":        {flavor.RancherControlPlaneGroup, flavor.RancherControlPlaneKind, strategies.ClusterCapabilities{ScaleControlPlane: true, ScaleWorkers: true}},
		"unknown kind":   {"example.com", unknownControlPlaneKind, strategies.ClusterCapabilities{}},
		"same kind name": {"example.com", controlPlaneKind, strategies.ClusterCapabilities{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cluster := &capiv1.Cluster{}
			cluster.Spec.ControlPlaneRef = capiv1.ContractVersionedObjectReference{APIGroup: tc.group, Kind: tc.kind, Name: "cp"}
			info := toInfo(cluster)
			assert.Equal(t, tc.want, info.Capabilities)
			assert.Equal(t, tc.kind, info.ControlPlaneKind)
		})
	}
}
