package cluster_manager_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/vatesfr/vmk-engine/pkg/components/cluster_manager"
	"github.com/vatesfr/vmk-engine/pkg/strategies"
	"github.com/vatesfr/vmk-engine/pkg/strategies/mocks"
)

const attachedNodeName = "worker-node"

func TestGetClusterVolumesResolvesAttachedNodes(t *testing.T) {
	ctrl := gomock.NewController(t)
	resources := mocks.NewMockForManagingClusterResources(ctrl)
	machines := mocks.NewMockForManagingClusterMachines(ctrl)
	workload := mocks.NewMockForManagingWorkloadResources(ctrl)
	vmID := uuid.Must(uuid.NewV4())
	resources.EXPECT().GetByUUID(gomock.Any(), clusterUUID).Return(strategies.ClusterInfo{Name: clusterName, ID: clusterUUID}, nil)
	workload.EXPECT().GetVolumes(gomock.Any(), clusterName).Return([]strategies.WorkloadVolume{
		{Name: "pv", AttachedNodes: []strategies.VolumeNode{{NodeName: attachedNodeName}, {NodeName: "foreign"}}},
	}, nil)
	machines.EXPECT().GetMachinesByCluster(gomock.Any(), clusterUUID).Return([]strategies.ClusterNodeInfo{
		{ID: deleteNodeUUID, NodeName: attachedNodeName, VMID: &vmID}, {Name: "registering"},
	}, nil)
	manager := cluster_manager.NewClusterManager(resources, nil, machines, nil, nil, workload)
	result, err := manager.GetClusterVolumes(t.Context(), clusterUUID)
	require.NoError(t, err)
	require.Equal(t, []strategies.VolumeNode{
		{NodeName: attachedNodeName, ID: &deleteNodeUUID, VMID: &vmID},
		{NodeName: "foreign"},
	}, result[0].AttachedNodes)
}

func TestGetClusterVolumesErrors(t *testing.T) {
	t.Run("cluster not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		resources := mocks.NewMockForManagingClusterResources(ctrl)
		resources.EXPECT().GetByUUID(gomock.Any(), clusterUUID).Return(strategies.ClusterInfo{}, strategies.ErrClusterNotFound)
		manager := cluster_manager.NewClusterManager(resources, nil, nil, nil, nil, mocks.NewMockForManagingWorkloadResources(ctrl))
		_, err := manager.GetClusterVolumes(t.Context(), clusterUUID)
		require.ErrorIs(t, err, strategies.ErrClusterNotFound)
	})
	t.Run("workload unavailable", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		resources := mocks.NewMockForManagingClusterResources(ctrl)
		workload := mocks.NewMockForManagingWorkloadResources(ctrl)
		resources.EXPECT().GetByUUID(gomock.Any(), clusterUUID).Return(strategies.ClusterInfo{Name: clusterName, ID: clusterUUID}, nil)
		workload.EXPECT().GetVolumes(gomock.Any(), clusterName).Return(nil, strategies.ErrWorkloadUnavailable)
		manager := cluster_manager.NewClusterManager(resources, nil, nil, nil, nil, workload)
		_, err := manager.GetClusterVolumes(t.Context(), clusterUUID)
		require.ErrorIs(t, err, strategies.ErrWorkloadUnavailable)
	})
}

func TestClusterEnrichmentIncludesVolumes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		volumes []strategies.WorkloadVolume
		err     error
		want    []strategies.WorkloadVolume
	}{
		{"available", []strategies.WorkloadVolume{{Name: "pv"}}, nil, []strategies.WorkloadVolume{{Name: "pv"}}},
		{"no volume", []strategies.WorkloadVolume{}, nil, []strategies.WorkloadVolume{}},
		{"offline", nil, errOffline, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			resources := mocks.NewMockForManagingClusterResources(ctrl)
			workload := mocks.NewMockForManagingWorkloadResources(ctrl)
			resources.EXPECT().List(gomock.Any()).Return([]strategies.ClusterInfo{{Name: clusterName, ID: clusterUUID}}, nil)
			workload.EXPECT().GetVolumes(gomock.Any(), clusterName).Return(tc.volumes, tc.err)
			workload.EXPECT().GetNodeStates(gomock.Any(), clusterName).Return(nil, errOffline).AnyTimes()
			manager := cluster_manager.NewClusterManager(resources, nil, nil, nil, nil, workload)
			clusters, err := manager.GetClusters(t.Context())
			require.NoError(t, err)
			require.Equal(t, tc.want, clusters[0].Volumes)
		})
	}
}
