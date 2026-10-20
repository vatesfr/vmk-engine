package cluster_manager_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"capv-proxy/pkg/components/cluster_manager"
	versionmanager "capv-proxy/pkg/components/version_manager"
	"capv-proxy/pkg/strategies"
	"capv-proxy/pkg/strategies/mocks"
	"capv-proxy/pkg/uuids"
)

type testCreateClusterParams struct {
	params                    strategies.CreateClusterParams
	clusterResourcesCreateErr error
	oracleErr                 error
}

const (
	deleteNodeName       = "node"
	clusterVersion       = "1.36.5"
	clusterName          = "test-cluster"
	secondClusterName    = "test-cluster-2"
	emptyClusterName     = ""
	controlPlaneIP       = "1.2.3.4"
	emptyControlPlaneIP  = ""
	controlPlaneReplicas = int32(1)
	workerReplicas       = int32(1)
	invalidReplicaCount  = int32(0)
	vmNamePrefix         = "test"
	emptyVmNamePrefix    = ""
	provisionedPhase     = "Provisioned"
	provisioningPhase    = "Provisioning"
	controlPlaneNodeName = "control-plane-0"
	workerNodeName       = "worker-0"
	controlPlaneRole     = "control-plane"
	workerRole           = "worker"
	readyStatus          = "Ready"
	controlPlaneNodeIP   = "1.2.3.5"
	workerNodeIP         = "1.2.3.6"
	clusterConfig        = "apiVersion: v1\nkind: Config"
)

var (
	errTest                 = errors.New("test error")
	clusterCreationDefaults = strategies.ClusterCreationDefaults{
		WorkerName:     "test-workers",
		EndpointPort:   6443,
		EndpointSubnet: 24,
		LoadBalancer:   "test-lb",
	}
	clusterUUID    = uuids.ClusterID(clusterName)
	deleteNodeUUID = uuids.NodeID(clusterName, deleteNodeName)
)

func TestCreateCluster(t *testing.T) {
	testCases := map[string]testCreateClusterParams{
		"all params provided": {
			params: strategies.CreateClusterParams{
				Version:              clusterVersion,
				Name:                 clusterName,
				ControlPlaneReplicas: controlPlaneReplicas,
				WorkerReplicas:       workerReplicas,
				ControlPlaneIP:       controlPlaneIP,
				VMNamePrefix:         vmNamePrefix,
			},
			clusterResourcesCreateErr: nil,
			oracleErr:                 nil,
		},
		"no vm name prefix": {
			params: strategies.CreateClusterParams{
				Version:              clusterVersion,
				Name:                 clusterName,
				ControlPlaneReplicas: controlPlaneReplicas,
				WorkerReplicas:       workerReplicas,
				ControlPlaneIP:       controlPlaneIP,
				VMNamePrefix:         emptyVmNamePrefix,
			},
			clusterResourcesCreateErr: nil,
			oracleErr:                 nil,
		},
		"missing cluster name": {
			params: strategies.CreateClusterParams{
				Version:              clusterVersion,
				Name:                 emptyClusterName,
				ControlPlaneReplicas: controlPlaneReplicas,
				WorkerReplicas:       workerReplicas,
				ControlPlaneIP:       controlPlaneIP,
				VMNamePrefix:         vmNamePrefix,
			},
			clusterResourcesCreateErr: nil,
			oracleErr:                 strategies.ErrClusterNameRequired,
		},
		"missing control plane replicas": {
			params: strategies.CreateClusterParams{
				Version:              clusterVersion,
				Name:                 clusterName,
				ControlPlaneReplicas: invalidReplicaCount,
				WorkerReplicas:       workerReplicas,
				ControlPlaneIP:       controlPlaneIP,
				VMNamePrefix:         vmNamePrefix,
			},
			clusterResourcesCreateErr: nil,
			oracleErr:                 strategies.ErrControlPlaneReplicasRequired,
		},
		"missing worker replicas": {
			params: strategies.CreateClusterParams{
				Version:              clusterVersion,
				Name:                 clusterName,
				ControlPlaneReplicas: controlPlaneReplicas,
				WorkerReplicas:       invalidReplicaCount,
				ControlPlaneIP:       controlPlaneIP,
				VMNamePrefix:         vmNamePrefix,
			},
			clusterResourcesCreateErr: nil,
			oracleErr:                 strategies.ErrWorkerReplicasRequired,
		},
		"missing control plane IP": {
			params: strategies.CreateClusterParams{
				Version:              clusterVersion,
				Name:                 clusterName,
				ControlPlaneReplicas: controlPlaneReplicas,
				WorkerReplicas:       workerReplicas,
				ControlPlaneIP:       emptyControlPlaneIP,
				VMNamePrefix:         vmNamePrefix,
			},
			clusterResourcesCreateErr: nil,
			oracleErr:                 strategies.ErrControlPlaneIPRequired,
		},
		"cluster resources create error": {
			params: strategies.CreateClusterParams{
				Version:              clusterVersion,
				Name:                 clusterName,
				ControlPlaneReplicas: controlPlaneReplicas,
				WorkerReplicas:       workerReplicas,
				ControlPlaneIP:       controlPlaneIP,
				VMNamePrefix:         vmNamePrefix,
			},
			clusterResourcesCreateErr: errTest,
			oracleErr:                 strategies.ErrClusterCreationFailed,
		},
		"cluster already exists": {
			params: strategies.CreateClusterParams{
				Version:              clusterVersion,
				Name:                 clusterName,
				ControlPlaneReplicas: controlPlaneReplicas,
				WorkerReplicas:       workerReplicas,
				ControlPlaneIP:       controlPlaneIP,
				VMNamePrefix:         vmNamePrefix,
			},
			clusterResourcesCreateErr: strategies.ErrClusterAlreadyExists,
			oracleErr:                 strategies.ErrClusterCreationFailed,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			clusterResources, _, _, clusterManager := newTestClusterManager(t)

			if tc.params.ControlPlaneReplicas > 0 &&
				tc.params.WorkerReplicas > 0 &&
				tc.params.Name != "" &&
				tc.params.ControlPlaneIP != "" {
				clusterResources.EXPECT().Create(t.Context(),
					gomock.AssignableToTypeOf(strategies.CreateClusterResourceParams{}),
				).Return(
					strategies.ClusterInfo{Name: tc.params.Name},
					tc.clusterResourcesCreateErr,
				)
			}
			_, err := clusterManager.CreateCluster(t.Context(), tc.params)

			if tc.oracleErr == nil {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.ErrorIs(t, err, tc.oracleErr)

			// Look for error propagation if clusterResourcesCreateErr
			if tc.clusterResourcesCreateErr != nil {
				assert.ErrorIs(t, err, tc.clusterResourcesCreateErr)
			}
		})
	}
}

func TestCreateClusterGetsDefaultsFromStrategy(t *testing.T) {
	ctrl := gomock.NewController(t)
	clusterResources := mocks.NewMockForManagingClusterResources(ctrl)
	clusterConfigs := mocks.NewMockForGettingClusterConfig(ctrl)
	clusterMachines := mocks.NewMockForManagingClusterMachines(ctrl)
	clusterDefaults := mocks.NewMockForGettingClusterCreationDefaults(ctrl)
	clusterManager := cluster_manager.NewClusterManager(clusterResources, clusterConfigs, clusterMachines, clusterDefaults, versionmanager.NewVersionManager())
	params := strategies.CreateClusterParams{
		Version:              clusterVersion,
		Name:                 clusterName,
		ControlPlaneReplicas: controlPlaneReplicas,
		WorkerReplicas:       workerReplicas,
		ControlPlaneIP:       controlPlaneIP,
		VMNamePrefix:         vmNamePrefix,
		Tags:                 map[string]string{"environment": "production"},
	}

	clusterDefaults.EXPECT().GetClusterCreationDefaults(t.Context()).Return(clusterCreationDefaults, nil)
	clusterResources.EXPECT().Create(t.Context(), strategies.CreateClusterResourceParams{
		Version:              "v" + clusterVersion,
		ControlPlaneIP:       params.ControlPlaneIP,
		VMNamePrefix:         params.VMNamePrefix,
		Name:                 clusterName,
		Defaults:             clusterCreationDefaults,
		ControlPlaneReplicas: &params.ControlPlaneReplicas,
		MachineDeployments: []strategies.MachineDeployment{{
			Name:     clusterCreationDefaults.WorkerName,
			Replicas: &params.WorkerReplicas,
		}},

		Tags: params.Tags,
	}).Return(strategies.ClusterInfo{Name: clusterName}, nil)

	_, err := clusterManager.CreateCluster(t.Context(), params)

	require.NoError(t, err)
}

func TestCreateClusterDefaultsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	clusterDefaults := mocks.NewMockForGettingClusterCreationDefaults(ctrl)
	clusterDefaults.EXPECT().GetClusterCreationDefaults(t.Context()).Return(strategies.ClusterCreationDefaults{}, errTest)
	clusterManager := cluster_manager.NewClusterManager(
		mocks.NewMockForManagingClusterResources(ctrl),
		mocks.NewMockForGettingClusterConfig(ctrl),
		mocks.NewMockForManagingClusterMachines(ctrl),
		clusterDefaults,
		versionmanager.NewVersionManager(),
	)

	_, err := clusterManager.CreateCluster(t.Context(), strategies.CreateClusterParams{
		Version:              clusterVersion,
		Name:                 clusterName,
		ControlPlaneReplicas: controlPlaneReplicas,
		WorkerReplicas:       workerReplicas,
		ControlPlaneIP:       controlPlaneIP,
	})

	assert.ErrorIs(t, err, errTest)
	assert.ErrorIs(t, err, strategies.ErrClusterCreationDefaultsRetrievalFailed)
}

func TestGetClusters(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		expected := []strategies.ClusterInfo{
			{Name: clusterName, Phase: provisionedPhase},
			{Name: secondClusterName, Phase: provisioningPhase},
		}
		clusterResources.EXPECT().List(t.Context()).Return(expected, nil)

		clusters, err := clusterManager.GetClusters(t.Context())

		require.NoError(t, err)
		assert.Equal(t, expected, clusters)
	})

	t.Run("error", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().List(t.Context()).Return(nil, errTest)

		clusters, err := clusterManager.GetClusters(t.Context())

		assert.Nil(t, clusters)
		assert.ErrorIs(t, err, errTest)
		assert.ErrorIs(t, err, strategies.ErrClusterListingFailed)
	})
}

func TestGetCluster(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		expected := strategies.ClusterInfo{
			Name:                 clusterName,
			Phase:                provisionedPhase,
			ControlPlaneEndpoint: controlPlaneIP,
		}
		clusterResources.EXPECT().GetByUUID(t.Context(), clusterUUID).Return(expected, nil)

		cluster, err := clusterManager.GetClusterByUUID(t.Context(), clusterUUID)

		require.NoError(t, err)
		assert.Equal(t, expected, cluster)
	})

	t.Run("error", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().GetByUUID(t.Context(), clusterUUID).Return(strategies.ClusterInfo{}, errTest)

		cluster, err := clusterManager.GetClusterByUUID(t.Context(), clusterUUID)

		assert.Empty(t, cluster)
		assert.ErrorIs(t, err, errTest)
		assert.ErrorIs(t, err, strategies.ErrClusterRetrievalFailed)
	})

	t.Run("not found", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().GetByUUID(t.Context(), clusterUUID).Return(strategies.ClusterInfo{}, strategies.ErrClusterNotFound)

		cluster, err := clusterManager.GetClusterByUUID(t.Context(), clusterUUID)

		assert.Empty(t, cluster)
		assert.ErrorIs(t, err, strategies.ErrClusterNotFound)
		assert.ErrorIs(t, err, strategies.ErrClusterRetrievalFailed)
	})
}

func TestGetClusterNodesInfo(t *testing.T) {
	clusterID := uuids.ClusterID(clusterName)
	clusterInfo := strategies.ClusterInfo{ID: clusterID, Name: clusterName}

	t.Run("success", func(t *testing.T) {
		clusterResources, _, clusterMachines, clusterManager := newTestClusterManager(t)
		expected := []strategies.ClusterNodeInfo{
			{Name: controlPlaneNodeName, Role: controlPlaneRole, Status: readyStatus, Endpoint: controlPlaneNodeIP},
			{Name: workerNodeName, Role: workerRole, Status: readyStatus, Endpoint: workerNodeIP},
		}
		clusterResources.EXPECT().GetByUUID(t.Context(), clusterID).Return(clusterInfo, nil)
		clusterMachines.EXPECT().GetMachinesByCluster(t.Context(), clusterID).Return(expected, nil)

		nodes, err := clusterManager.GetClusterNodesInfo(t.Context(), clusterID)

		require.NoError(t, err)
		assert.Equal(t, expected, nodes)
	})

	t.Run("cluster not found", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().GetByUUID(t.Context(), clusterID).Return(strategies.ClusterInfo{}, strategies.ErrClusterNotFound)

		nodes, err := clusterManager.GetClusterNodesInfo(t.Context(), clusterID)

		assert.Nil(t, nodes)
		assert.ErrorIs(t, err, strategies.ErrClusterNotFound)
	})

	t.Run("error", func(t *testing.T) {
		clusterResources, _, clusterMachines, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().GetByUUID(t.Context(), clusterID).Return(clusterInfo, nil)
		clusterMachines.EXPECT().GetMachinesByCluster(t.Context(), clusterID).Return(nil, errTest)

		nodes, err := clusterManager.GetClusterNodesInfo(t.Context(), clusterID)

		assert.Nil(t, nodes)
		assert.ErrorIs(t, err, errTest)
		assert.ErrorIs(t, err, strategies.ErrClusterNodesRetrievalFailed)
	})
}

func TestGetClusterConfig(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		clusterResources, clusterConfigs, _, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().GetByUUID(t.Context(), clusterUUID).Return(strategies.ClusterInfo{Name: clusterName}, nil)
		clusterConfigs.EXPECT().Get(t.Context(), clusterName).Return(clusterConfig, nil)

		config, err := clusterManager.GetClusterConfig(t.Context(), clusterUUID)

		require.NoError(t, err)
		assert.Equal(t, clusterConfig, config)
	})

	t.Run("error", func(t *testing.T) {
		clusterResources, clusterConfigs, _, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().GetByUUID(t.Context(), clusterUUID).Return(strategies.ClusterInfo{Name: clusterName}, nil)
		clusterConfigs.EXPECT().Get(t.Context(), clusterName).Return("", errTest)

		config, err := clusterManager.GetClusterConfig(t.Context(), clusterUUID)

		assert.Empty(t, config)
		assert.ErrorIs(t, err, errTest)
		assert.ErrorIs(t, err, strategies.ErrClusterConfigRetrievalFailed)
	})
}

func TestDeleteCluster(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().Delete(t.Context(), clusterUUID).Return(nil)

		err := clusterManager.DeleteCluster(t.Context(), clusterUUID)

		require.NoError(t, err)
	})

	t.Run("error", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().Delete(t.Context(), clusterUUID).Return(errTest)

		err := clusterManager.DeleteCluster(t.Context(), clusterUUID)

		assert.ErrorIs(t, err, errTest)
		assert.ErrorIs(t, err, strategies.ErrClusterDeletionFailed)
	})
}

func TestClusterNameRequired(t *testing.T) {
	testCases := map[string]func(cluster_manager.ClusterManager) error{
		"get cluster": func(manager cluster_manager.ClusterManager) error {
			_, err := manager.GetClusterByUUID(t.Context(), uuid.Nil)
			return err
		},
		"get cluster config": func(manager cluster_manager.ClusterManager) error {
			_, err := manager.GetClusterConfig(t.Context(), uuid.Nil)
			return err
		},
		"delete cluster": func(manager cluster_manager.ClusterManager) error {
			return manager.DeleteCluster(t.Context(), uuid.Nil)
		},
	}

	for name, run := range testCases {
		t.Run(name, func(t *testing.T) {
			clusterResources, _, _, clusterManager := newTestClusterManager(t)
			if name == "get cluster" {
				clusterResources.EXPECT().GetByUUID(gomock.Any(), uuid.Nil).Return(strategies.ClusterInfo{}, strategies.ErrClusterIDRequired)
			}
			err := run(clusterManager)

			assert.ErrorIs(t, err, strategies.ErrClusterIDRequired)
		})
	}
}

func newTestClusterManager(t *testing.T) (
	*mocks.MockForManagingClusterResources,
	*mocks.MockForGettingClusterConfig,
	*mocks.MockForManagingClusterMachines,
	cluster_manager.ClusterManager,
) {
	t.Helper()

	ctrl := gomock.NewController(t)
	clusterResources := mocks.NewMockForManagingClusterResources(ctrl)
	clusterConfigs := mocks.NewMockForGettingClusterConfig(ctrl)
	clusterMachines := mocks.NewMockForManagingClusterMachines(ctrl)
	clusterDefaults := mocks.NewMockForGettingClusterCreationDefaults(ctrl)
	clusterDefaults.EXPECT().GetClusterCreationDefaults(gomock.Any()).Return(clusterCreationDefaults, nil).AnyTimes()
	clusterManager := cluster_manager.NewClusterManager(clusterResources, clusterConfigs, clusterMachines, clusterDefaults, versionmanager.NewVersionManager())

	return clusterResources, clusterConfigs, clusterMachines, clusterManager
}

func TestGetNodeByUUID(t *testing.T) {
	nodeID := uuids.NodeID(clusterName, controlPlaneNodeName)
	expectedNode := strategies.ClusterNodeInfo{ID: nodeID, Name: controlPlaneNodeName, Role: controlPlaneRole, Status: readyStatus}

	t.Run("success", func(t *testing.T) {
		_, _, machines, manager := newTestClusterManager(t)
		machines.EXPECT().GetMachineByUUID(t.Context(), nodeID).Return(expectedNode, nil)

		node, err := manager.GetNodeByUUID(t.Context(), nodeID)

		require.NoError(t, err)
		assert.Equal(t, expectedNode, node)
	})

	t.Run("error", func(t *testing.T) {
		_, _, machines, manager := newTestClusterManager(t)
		machines.EXPECT().GetMachineByUUID(t.Context(), nodeID).Return(strategies.ClusterNodeInfo{}, errTest)

		node, err := manager.GetNodeByUUID(t.Context(), nodeID)

		assert.Empty(t, node)
		require.Error(t, err)
		assert.ErrorIs(t, err, errTest)
	})
}

func TestGetAllNodes(t *testing.T) {
	testCases := map[string]struct {
		nodes []strategies.ClusterNodeInfo
		err   error
	}{
		"success": {nodes: []strategies.ClusterNodeInfo{
			{Name: controlPlaneNodeName, Role: controlPlaneRole, Status: readyStatus, Endpoint: controlPlaneNodeIP},
			{Name: workerNodeName, Role: workerRole, Status: readyStatus, Endpoint: workerNodeIP},
		}},
		"empty result": {nodes: []strategies.ClusterNodeInfo{}},
		"retrieval error discards partial result": {
			nodes: []strategies.ClusterNodeInfo{{Name: workerNodeName}}, err: errTest,
		},
		"missing namespace": {err: strategies.ErrClusterNamespaceRequired},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, machines, manager := newTestClusterManager(t)
			machines.EXPECT().GetAllMachines(t.Context()).Return(tc.nodes, tc.err)

			nodes, err := manager.GetAllNodes(t.Context())

			if tc.err != nil {
				assert.Nil(t, nodes)
				assert.ErrorIs(t, err, strategies.ErrClusterNodesRetrievalFailed)
				assert.ErrorIs(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.nodes, nodes)
		})
	}
}

func TestCreateClusterDescription(t *testing.T) {
	for _, tc := range []struct {
		name        string
		description string
		isValid     bool
	}{
		{"empty", "", true},
		{"special characters", "Cluster \"Backend\"\nÉquipe 🚀", true},
		{"unicode boundary", strings.Repeat("🚀", strategies.MaxClusterDescriptionLength), true},
		{"too long", strings.Repeat("é", strategies.MaxClusterDescriptionLength+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources, _, _, manager := newTestClusterManager(t)
			params := strategies.CreateClusterParams{Version: clusterVersion, Name: clusterName, Description: tc.description, ControlPlaneReplicas: 1, WorkerReplicas: 1, ControlPlaneIP: controlPlaneIP}
			if tc.isValid {
				resources.EXPECT().Create(t.Context(), gomock.Any()).DoAndReturn(func(
					_ context.Context, resourceParams strategies.CreateClusterResourceParams,
				) (strategies.ClusterInfo, error) {
					assert.Equal(t, tc.description, resourceParams.Description)
					return strategies.ClusterInfo{Name: clusterName, Description: resourceParams.Description}, nil
				})
			}
			info, err := manager.CreateCluster(t.Context(), params)
			if !tc.isValid {
				require.ErrorIs(t, err, strategies.ErrClusterDescriptionTooLong)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.description, info.Description)
			}
		})
	}
}

func TestDeleteClusterNode(t *testing.T) {
	for name, tc := range map[string]struct {
		cluster, node              uuid.UUID
		getErr, deleteErr, wantErr error
	}{
		"success":                   {cluster: clusterUUID, node: deleteNodeUUID},
		"cluster required":          {node: deleteNodeUUID, wantErr: strategies.ErrClusterIDRequired},
		"node required":             {cluster: clusterUUID, wantErr: strategies.ErrNodeIDRequired},
		"missing cluster":           {cluster: clusterUUID, node: deleteNodeUUID, getErr: strategies.ErrClusterNotFound, wantErr: strategies.ErrClusterNotFound},
		"cluster retrieval failure": {cluster: clusterUUID, node: deleteNodeUUID, getErr: errTest, wantErr: errTest},
		"missing node":              {cluster: clusterUUID, node: deleteNodeUUID, deleteErr: strategies.ErrNodeNotFound, wantErr: strategies.ErrNodeNotFound},
		"deletion failure":          {cluster: clusterUUID, node: deleteNodeUUID, deleteErr: errTest, wantErr: errTest},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			resources := mocks.NewMockForManagingClusterResources(ctrl)
			nodes := mocks.NewMockForManagingClusterMachines(ctrl)
			if tc.cluster != uuid.Nil && tc.node != uuid.Nil {
				get := resources.EXPECT().GetByUUID(t.Context(), tc.cluster).Return(strategies.ClusterInfo{}, tc.getErr)
				if tc.getErr == nil {
					nodes.EXPECT().DeleteMachine(t.Context(), tc.cluster, tc.node).After(get).Return(tc.deleteErr)
				}
			}
			err := cluster_manager.NewClusterManager(resources, nil, nodes, nil, nil).DeleteClusterNode(t.Context(), tc.cluster, tc.node)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}
			if tc.getErr != nil || tc.deleteErr != nil {
				require.ErrorIs(t, err, strategies.ErrNodeDeletionFailed)
			}
		})
	}
}

func TestCreateClusterRejectsInvalidParametersBeforeReadingDefaults(t *testing.T) {
	testCases := []struct {
		name    string
		version string
		want    error
	}{
		{clusterName, "", strategies.ErrClusterVersionRequired},
		{clusterName, "1.36.99", strategies.ErrClusterVersionUnavailable},
		{clusterName, "1.36", strategies.ErrClusterVersionUnavailable},
		{clusterName, "v1.36.9", strategies.ErrClusterVersionUnavailable},
		{clusterName, " 1.36.9", strategies.ErrClusterVersionUnavailable},
		{"NHT-Kub-Prod", clusterVersion, strategies.ErrInvalidClusterName},
		{"cluster_name", clusterVersion, strategies.ErrInvalidClusterName},
		{"cluster.name", clusterVersion, strategies.ErrInvalidClusterName},
		{"-cluster", clusterVersion, strategies.ErrInvalidClusterName},
		{"cluster-", clusterVersion, strategies.ErrInvalidClusterName},
		{" cluster", clusterVersion, strategies.ErrInvalidClusterName},
		{strings.Repeat("a", 64), clusterVersion, strategies.ErrInvalidClusterName},
	}
	for _, tc := range testCases {
		t.Run(tc.name+"/"+tc.version, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			// No calls to defaults or resources are expected for invalid parameters.
			manager := cluster_manager.NewClusterManager(
				mocks.NewMockForManagingClusterResources(ctrl), nil, nil,
				mocks.NewMockForGettingClusterCreationDefaults(ctrl), versionmanager.NewVersionManager(),
			)
			result, err := manager.CreateCluster(t.Context(), strategies.CreateClusterParams{
				Name: tc.name, Version: tc.version, ControlPlaneIP: controlPlaneIP,
				ControlPlaneReplicas: controlPlaneReplicas, WorkerReplicas: workerReplicas,
			})
			assert.Empty(t, result)
			require.ErrorIs(t, err, tc.want)
			if tc.want == strategies.ErrInvalidClusterName {
				assert.Contains(t, err.Error(), tc.name)
			}
		})
	}
}

func TestCreateClusterUsesInjectedVersionStrategy(t *testing.T) {
	ctrl := gomock.NewController(t)
	versions := mocks.NewMockForManagingVersions(ctrl)
	defaults := mocks.NewMockForGettingClusterCreationDefaults(ctrl)
	resources := mocks.NewMockForManagingClusterResources(ctrl)
	manager := cluster_manager.NewClusterManager(resources, nil, nil, defaults, versions)
	params := strategies.CreateClusterParams{
		Name: clusterName, Version: "1.99.1", ControlPlaneIP: controlPlaneIP,
		ControlPlaneReplicas: controlPlaneReplicas, WorkerReplicas: workerReplicas,
	}
	gomock.InOrder(
		versions.EXPECT().IsVersionAvailable(params.Version).Return(true),
		defaults.EXPECT().GetClusterCreationDefaults(t.Context()).Return(clusterCreationDefaults, nil),
		resources.EXPECT().Create(t.Context(), gomock.Any()).DoAndReturn(
			func(_ context.Context, received strategies.CreateClusterResourceParams) (strategies.ClusterInfo, error) {
				assert.Equal(t, "v1.99.1", received.Version)
				return strategies.ClusterInfo{Name: clusterName}, nil
			},
		),
	)
	_, err := manager.CreateCluster(t.Context(), params)
	require.NoError(t, err)
}

func TestSetPaused(t *testing.T) {
	for _, paused := range []bool{true, false} {
		ctrl := gomock.NewController(t)
		resources := mocks.NewMockForManagingClusterResources(ctrl)
		machines := mocks.NewMockForManagingClusterMachines(ctrl)
		manager := cluster_manager.NewClusterManager(resources, nil, machines, nil, nil)
		resources.EXPECT().SetPaused(t.Context(), clusterUUID, paused).Return(nil)
		require.NoError(t, manager.SetClusterPaused(t.Context(), clusterUUID, paused))
		gomock.InOrder(
			resources.EXPECT().GetByUUID(t.Context(), clusterUUID).Return(strategies.ClusterInfo{}, nil),
			machines.EXPECT().SetMachinePaused(t.Context(), clusterUUID, deleteNodeUUID, paused).Return(nil),
		)
		require.NoError(t, manager.SetClusterNodePaused(t.Context(), clusterUUID, deleteNodeUUID, paused))
		require.ErrorIs(t, manager.SetClusterPaused(t.Context(), uuid.Nil, paused), strategies.ErrClusterIDRequired)
		require.ErrorIs(t, manager.SetClusterNodePaused(t.Context(), uuid.Nil, deleteNodeUUID, paused), strategies.ErrClusterIDRequired)
		require.ErrorIs(t, manager.SetClusterNodePaused(t.Context(), clusterUUID, uuid.Nil, paused), strategies.ErrNodeIDRequired)
		resources.EXPECT().GetByUUID(t.Context(), clusterUUID).Return(strategies.ClusterInfo{}, strategies.ErrClusterNotFound)
		require.ErrorIs(t, manager.SetClusterNodePaused(t.Context(), clusterUUID, deleteNodeUUID, paused), strategies.ErrClusterNotFound)
		resources.EXPECT().SetPaused(t.Context(), clusterUUID, paused).Return(strategies.ErrPauseUpdateConflict)
		require.ErrorIs(t, manager.SetClusterPaused(t.Context(), clusterUUID, paused), strategies.ErrPauseUpdateConflict)
		resources.EXPECT().GetByUUID(t.Context(), clusterUUID).Return(strategies.ClusterInfo{}, nil)
		machines.EXPECT().SetMachinePaused(t.Context(), clusterUUID, deleteNodeUUID, paused).Return(strategies.ErrPauseUpdateConflict)
		require.ErrorIs(t, manager.SetClusterNodePaused(t.Context(), clusterUUID, deleteNodeUUID, paused), strategies.ErrPauseUpdateConflict)
	}
}

func TestSetClusterDescription(t *testing.T) {
	for _, tc := range []struct {
		name, description    string
		resourceErr, wantErr error
	}{
		{name: clusterName, description: "Équipe 🚀\nBackend"},
		{name: clusterName},
		{name: clusterName, description: strings.Repeat("🚀", strategies.MaxClusterDescriptionLength)},
		{name: clusterName, description: strings.Repeat("é", strategies.MaxClusterDescriptionLength+1), wantErr: strategies.ErrClusterDescriptionTooLong},
		{wantErr: strategies.ErrClusterNameRequired},
		{name: clusterName, resourceErr: strategies.ErrClusterNotFound, wantErr: strategies.ErrClusterNotFound},
		{name: clusterName, resourceErr: strategies.ErrClusterDescriptionUpdateConflict, wantErr: strategies.ErrClusterDescriptionUpdateConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources, _, _, manager := newTestClusterManager(t)
			if tc.wantErr == nil || tc.resourceErr != nil {
				resources.EXPECT().SetDescription(t.Context(), tc.name, tc.description).Return(tc.resourceErr)
			}
			err := manager.SetClusterDescription(t.Context(), tc.name, tc.description)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
				if tc.resourceErr != nil {
					require.ErrorIs(t, err, strategies.ErrClusterDescriptionUpdateFailed)
				}
			}
		})
	}
}
