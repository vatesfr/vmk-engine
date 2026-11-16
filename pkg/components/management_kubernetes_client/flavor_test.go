package management_kubernetes_client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vatesfr/vmk-engine/pkg/strategies"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const unknownControlPlaneKind = "UnknownControlPlane"

func TestUnsupportedFlavor(t *testing.T) {
	cluster, md, cp := scalingObjects()
	cluster.Spec.ControlPlaneRef.Kind = unknownControlPlaneKind
	k8s := newFakeClient(t, cluster, md, cp)
	service := NewClusterClient(k8s, clientAsCache{k8s}, testNamespace)

	// Its owner may re-render the MachineDeployment.
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
