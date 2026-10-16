package cluster_manager_test

import (
	"context"
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
		{Name: "m1", NodeName: "n1", ParentCluster: uuids.ClusterID(clusterName)},
		{Name: "m2", NodeName: "n2", ParentCluster: uuids.ClusterID(clusterName)},
		{Name: "m3", NodeName: "n3", ParentCluster: uuids.ClusterID(secondClusterName)},
	}, nil)
	states.EXPECT().GetNodeStates(gomock.Any(), clusterName).Return(map[string]strategies.WorkloadNodeState{
		"n1": {IsCordon: ptr.To(true), IsDraining: ptr.To(true)}, "n2": {IsCordon: ptr.To(false), IsDraining: ptr.To(false)},
	}, nil).Times(1)
	states.EXPECT().GetNodeStates(gomock.Any(), secondClusterName).Return(nil, errors.New("offline"))
	resources := mocks.NewMockForManagingClusterResources(ctrl)
	resources.EXPECT().GetByUUID(gomock.Any(), uuids.ClusterID(clusterName)).Return(strategies.ClusterInfo{Name: clusterName}, nil)
	resources.EXPECT().GetByUUID(gomock.Any(), uuids.ClusterID(secondClusterName)).Return(strategies.ClusterInfo{Name: secondClusterName}, nil)
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

// Both calls must start before either is released, without relying on timings.
func TestEnrichmentReadsClustersConcurrentlyAndPreservesOrder(t *testing.T) {
	for _, collection := range []string{"clusters", "nodes"} {
		t.Run(collection, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			resources := mocks.NewMockForManagingClusterResources(ctrl)
			machines := mocks.NewMockForManagingClusterMachines(ctrl)
			states := mocks.NewMockForGettingWorkloadNodeStates(ctrl)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			started := make(chan string, 2)
			release := make(chan struct{})
			defer close(release)
			read := func(ctx context.Context, name string) (map[string]strategies.WorkloadNodeState, error) {
				started <- name
				select {
				case <-release:
					value := name == clusterName
					return map[string]strategies.WorkloadNodeState{
						"n1": {IsCordon: ptr.To(value), IsDraining: ptr.To(false)},
						"n2": {IsCordon: ptr.To(value), IsDraining: ptr.To(false)},
					}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			states.EXPECT().GetNodeStates(gomock.Any(), clusterName).DoAndReturn(read).Times(1)
			states.EXPECT().GetNodeStates(gomock.Any(), secondClusterName).DoAndReturn(read).Times(1)
			manager := cluster_manager.NewClusterManager(resources, nil, nil, nil, nil, nil, states)
			type result struct {
				names   []string
				cordons []*bool
				err     error
			}
			var run func() result
			var wantNames []string
			var wantCordons []*bool
			if collection == "clusters" {
				resources.EXPECT().List(ctx).Return([]strategies.ClusterInfo{{Name: clusterName}, {Name: secondClusterName}}, nil)
				wantNames = []string{clusterName, secondClusterName}
				wantCordons = []*bool{ptr.To(true), ptr.To(false)}
				run = func() result {
					clusters, err := manager.GetClusters(ctx)
					output := result{err: err}
					for _, cluster := range clusters {
						output.names = append(output.names, cluster.Name)
						output.cordons = append(output.cordons, cluster.IsCordon)
					}
					return output
				}
			} else {
				resources.EXPECT().GetByUUID(gomock.Any(), clusterUUID).Return(strategies.ClusterInfo{Name: clusterName, ID: clusterUUID}, nil)
				resources.EXPECT().GetByUUID(gomock.Any(), uuids.ClusterID(secondClusterName)).Return(strategies.ClusterInfo{Name: secondClusterName, ID: uuids.ClusterID(secondClusterName)}, nil)
				manager = cluster_manager.NewClusterManager(resources, nil, machines, nil, nil, nil, states)
				machines.EXPECT().GetAllMachines(ctx).Return([]strategies.ClusterNodeInfo{
					{Name: "m1", NodeName: "n1", ParentCluster: clusterUUID},
					{Name: "m2", NodeName: "n1", ParentCluster: uuids.ClusterID(secondClusterName)},
					{Name: "m3", NodeName: "n2", ParentCluster: clusterUUID},
				}, nil)
				wantNames = []string{"m1", "m2", "m3"}
				wantCordons = []*bool{ptr.To(true), ptr.To(false), ptr.To(true)}
				run = func() result {
					nodes, err := manager.GetAllNodes(ctx)
					output := result{err: err}
					for _, node := range nodes {
						output.names = append(output.names, node.Name)
						output.cordons = append(output.cordons, node.IsCordon)
					}
					return output
				}
			}
			done := make(chan result, 1)
			go func() { done <- run() }()
			names := make([]string, 0, 2)
			for range 2 {
				select {
				case name := <-started:
					names = append(names, name)
				case <-ctx.Done():
					t.Fatal("cluster reads did not start concurrently")
				}
			}
			require.ElementsMatch(t, []string{clusterName, secondClusterName}, names)
			// Release both reads after verifying they started concurrently.
			release <- struct{}{}
			release <- struct{}{}
			select {
			case output := <-done:
				require.NoError(t, output.err)
				require.Equal(t, wantNames, output.names)
				require.Equal(t, wantCordons, output.cordons)
			case <-ctx.Done():
				t.Fatal("enrichment did not complete")
			}
		})
	}
}
