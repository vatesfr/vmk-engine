package cluster_manager_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"capv-proxy/pkg/components/cluster_manager"
	uuids "capv-proxy/pkg/helpers/uuids"
	"capv-proxy/pkg/strategies"
	"capv-proxy/pkg/strategies/mocks"

	"k8s.io/utils/ptr"
)

const (
	cordonAction      = "cordon"
	nodeActionCluster = "cluster"
)

func TestNodeOperationsResolveMachineBeforeCallingWorkload(t *testing.T) {
	for _, action := range []string{cordonAction, "uncordon", "drain"} {
		t.Run(action, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			resources := mocks.NewMockForManagingClusterResources(ctrl)
			machines := mocks.NewMockForManagingClusterMachines(ctrl)
			workload := mocks.NewMockForManagingWorkloadNodes(ctrl)
			resources.EXPECT().GetByUUID(gomock.Any(), clusterUUID).Return(strategies.ClusterInfo{Name: nodeActionCluster, ID: clusterUUID}, nil)
			machines.EXPECT().GetNodeName(gomock.Any(), clusterUUID, deleteNodeUUID).Return("node", nil)
			manager := cluster_manager.NewClusterManager(resources, nil, machines, nil, nil, workload, nil)
			if action == "drain" {
				options := strategies.DrainOptions{Timeout: time.Minute, IgnoreDaemonSets: true}
				workload.EXPECT().Drain(gomock.Any(), nodeActionCluster, "node", options)
				require.NoError(t, manager.DrainClusterNode(t.Context(), clusterUUID, deleteNodeUUID, options))
			} else {
				workload.EXPECT().SetCordon(gomock.Any(), nodeActionCluster, "node", action == cordonAction)
				require.NoError(t, manager.SetClusterNodeCordon(t.Context(), clusterUUID, deleteNodeUUID, action == cordonAction))
			}
		})
	}
}

func TestNodeOperationStopsWhenMachineCannotBeResolved(t *testing.T) {
	ctrl := gomock.NewController(t)
	resources := mocks.NewMockForManagingClusterResources(ctrl)
	machines := mocks.NewMockForManagingClusterMachines(ctrl)
	workload := mocks.NewMockForManagingWorkloadNodes(ctrl)
	resources.EXPECT().GetByUUID(gomock.Any(), clusterUUID).Return(strategies.ClusterInfo{Name: nodeActionCluster, ID: clusterUUID}, nil)
	machines.EXPECT().GetNodeName(gomock.Any(), clusterUUID, deleteNodeUUID).Return("", strategies.ErrNodeNotRegistered)
	manager := cluster_manager.NewClusterManager(resources, nil, machines, nil, nil, workload, nil)
	require.ErrorIs(t, manager.DrainClusterNode(t.Context(), clusterUUID, deleteNodeUUID, strategies.DrainOptions{}), strategies.ErrNodeNotRegistered)
}

func TestNodeEnrichmentGroupsReadsAndPreservesUnknown(t *testing.T) {
	ctrl := gomock.NewController(t)
	machines := mocks.NewMockForManagingClusterMachines(ctrl)
	states := mocks.NewMockForGettingWorkloadNodeStates(ctrl)
	machines.EXPECT().GetAllMachines(gomock.Any()).Return([]strategies.ClusterNodeInfo{
		{Name: "m1", NodeName: "n1", ParentCluster: uuids.ClusterID("one")},
		{Name: "m2", NodeName: "n2", ParentCluster: uuids.ClusterID("one")},
		{Name: "m3", NodeName: "n3", ParentCluster: uuids.ClusterID("two")},
	}, nil)
	states.EXPECT().GetNodeStates(gomock.Any(), "one").Return(map[string]strategies.WorkloadNodeState{
		"n1": {IsCordon: ptr.To(true), IsDraining: ptr.To(true)}, "n2": {IsCordon: ptr.To(false), IsDraining: ptr.To(false)},
	}, nil).Times(1)
	states.EXPECT().GetNodeStates(gomock.Any(), "two").Return(nil, errors.New("offline"))
	resources := mocks.NewMockForManagingClusterResources(ctrl)
	resources.EXPECT().GetByUUID(gomock.Any(), uuids.ClusterID("one")).Return(strategies.ClusterInfo{Name: "one"}, nil)
	resources.EXPECT().GetByUUID(gomock.Any(), uuids.ClusterID("two")).Return(strategies.ClusterInfo{Name: "two"}, nil)
	manager := cluster_manager.NewClusterManager(resources, nil, machines, nil, nil, nil, states)
	nodes, err := manager.GetAllNodes(t.Context())
	require.NoError(t, err)
	require.Equal(t, ptr.To(true), nodes[0].IsDraining)
	require.Equal(t, ptr.To(false), nodes[1].IsCordon)
	require.Nil(t, nodes[2].IsCordon)
	require.Nil(t, nodes[2].IsDraining)
}

func TestClusterStateAggregation(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		states                   map[string]strategies.WorkloadNodeState
		wantCordon, wantDraining *bool
	}{
		{"known false", map[string]strategies.WorkloadNodeState{"n1": {IsCordon: ptr.To(false), IsDraining: ptr.To(false)}}, ptr.To(false), ptr.To(false)},
		{"true wins over unknown", map[string]strategies.WorkloadNodeState{"n1": {IsCordon: ptr.To(true), IsDraining: ptr.To(true)}, "n2": {}}, ptr.To(true), ptr.To(true)},
		{"unknown prevents false", map[string]strategies.WorkloadNodeState{"n1": {IsCordon: ptr.To(false), IsDraining: ptr.To(false)}, "n2": {}}, nil, nil},
		{"partial pod read", map[string]strategies.WorkloadNodeState{"n1": {IsCordon: ptr.To(true)}}, ptr.To(true), nil},
		{"offline", nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			resources := mocks.NewMockForManagingClusterResources(ctrl)
			states := mocks.NewMockForGettingWorkloadNodeStates(ctrl)
			resources.EXPECT().GetByUUID(gomock.Any(), clusterUUID).Return(strategies.ClusterInfo{Name: nodeActionCluster, ID: clusterUUID}, nil)
			states.EXPECT().GetNodeStates(gomock.Any(), nodeActionCluster).Return(tc.states, nil)
			manager := cluster_manager.NewClusterManager(resources, nil, nil, nil, nil, nil, states)
			cluster, err := manager.GetClusterByUUID(t.Context(), clusterUUID)
			require.NoError(t, err)
			require.Equal(t, tc.wantCordon, cluster.IsCordon)
			require.Equal(t, tc.wantDraining, cluster.IsDraining)
		})
	}
}

func TestClusterAggregationOmitsFalseWhenMachineHasNoNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	resources := mocks.NewMockForManagingClusterResources(ctrl)
	states := mocks.NewMockForGettingWorkloadNodeStates(ctrl)
	machines := mocks.NewMockForManagingClusterMachines(ctrl)
	resources.EXPECT().GetByUUID(gomock.Any(), clusterUUID).Return(strategies.ClusterInfo{Name: nodeActionCluster, ID: clusterUUID}, nil)
	states.EXPECT().GetNodeStates(gomock.Any(), nodeActionCluster).Return(map[string]strategies.WorkloadNodeState{
		"node": {IsCordon: ptr.To(false), IsDraining: ptr.To(false)},
	}, nil)
	machines.EXPECT().GetMachinesByCluster(gomock.Any(), clusterUUID).Return([]strategies.ClusterNodeInfo{{NodeName: "node"}, {Name: "registering"}}, nil)
	manager := cluster_manager.NewClusterManager(resources, nil, machines, nil, nil, nil, states)
	cluster, err := manager.GetClusterByUUID(t.Context(), clusterUUID)
	require.NoError(t, err)
	require.Nil(t, cluster.IsCordon)
	require.Nil(t, cluster.IsDraining)
}
