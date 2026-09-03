package cluster_manager_test

import (
	"capv-proxy/pkg/components/cluster_manager"
	"capv-proxy/pkg/strategies"
	"capv-proxy/pkg/strategies/mocks"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type testCreateClusterParams struct {
	params                    strategies.CreateClusterParams
	clusterResourcesCreateErr error
	oracleErr                 error
}

const (
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

var testError = errors.New("test error")

func TestCreateCluster(t *testing.T) {
	testCases := map[string]testCreateClusterParams{
		"all params provided": {
			params: strategies.CreateClusterParams{
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
				Name:                 emptyClusterName,
				ControlPlaneReplicas: controlPlaneReplicas,
				WorkerReplicas:       workerReplicas,
				ControlPlaneIP:       controlPlaneIP,
				VMNamePrefix:         vmNamePrefix,
			},
			clusterResourcesCreateErr: nil,
			oracleErr:                 cluster_manager.ErrClusterNameRequired,
		},
		"missing control plane replicas": {
			params: strategies.CreateClusterParams{
				Name:                 clusterName,
				ControlPlaneReplicas: invalidReplicaCount,
				WorkerReplicas:       workerReplicas,
				ControlPlaneIP:       controlPlaneIP,
				VMNamePrefix:         vmNamePrefix,
			},
			clusterResourcesCreateErr: nil,
			oracleErr:                 cluster_manager.ErrControlPlaneReplicasRequired,
		},
		"missing worker replicas": {
			params: strategies.CreateClusterParams{
				Name:                 clusterName,
				ControlPlaneReplicas: controlPlaneReplicas,
				WorkerReplicas:       invalidReplicaCount,
				ControlPlaneIP:       controlPlaneIP,
				VMNamePrefix:         vmNamePrefix,
			},
			clusterResourcesCreateErr: nil,
			oracleErr:                 cluster_manager.ErrWorkerReplicasRequired,
		},
		"missing control plane IP": {
			params: strategies.CreateClusterParams{
				Name:                 clusterName,
				ControlPlaneReplicas: controlPlaneReplicas,
				WorkerReplicas:       workerReplicas,
				ControlPlaneIP:       emptyControlPlaneIP,
				VMNamePrefix:         vmNamePrefix,
			},
			clusterResourcesCreateErr: nil,
			oracleErr:                 cluster_manager.ErrControlPlaneIPRequired,
		},
		"cluster resources create error": {
			params: strategies.CreateClusterParams{
				Name:                 clusterName,
				ControlPlaneReplicas: controlPlaneReplicas,
				WorkerReplicas:       workerReplicas,
				ControlPlaneIP:       controlPlaneIP,
				VMNamePrefix:         vmNamePrefix,
			},
			clusterResourcesCreateErr: testError,
			oracleErr:                 cluster_manager.ErrClusterCreationFailed,
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
				clusterResources.EXPECT().Create(
					gomock.AssignableToTypeOf(strategies.CreateClusterResourceParams{}),
				).Return(
					strategies.ClusterInfo{Name: tc.params.Name},
					tc.clusterResourcesCreateErr,
				)
			}
			_, err := clusterManager.CreateCluster(tc.params)

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

func TestGetClusters(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		expected := []strategies.ClusterInfo{
			{Name: clusterName, Phase: provisionedPhase},
			{Name: secondClusterName, Phase: provisioningPhase},
		}
		clusterResources.EXPECT().List().Return(expected, nil)

		clusters, err := clusterManager.GetClusters()

		require.NoError(t, err)
		assert.Equal(t, expected, clusters)
	})

	t.Run("error", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().List().Return(nil, testError)

		clusters, err := clusterManager.GetClusters()

		assert.Nil(t, clusters)
		assert.ErrorIs(t, err, testError)
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
		clusterResources.EXPECT().Get(clusterName).Return(expected, nil)

		cluster, err := clusterManager.GetCluster(clusterName)

		require.NoError(t, err)
		assert.Equal(t, expected, cluster)
	})

	t.Run("error", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().Get(clusterName).Return(strategies.ClusterInfo{}, testError)

		cluster, err := clusterManager.GetCluster(clusterName)

		assert.Empty(t, cluster)
		assert.ErrorIs(t, err, testError)
	})
}

func TestGetClusterNodesInfo(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		_, _, clusterMachines, clusterManager := newTestClusterManager(t)
		expected := []strategies.ClusterNodeInfo{
			{Name: controlPlaneNodeName, Role: controlPlaneRole, Status: readyStatus, Endpoint: controlPlaneNodeIP},
			{Name: workerNodeName, Role: workerRole, Status: readyStatus, Endpoint: workerNodeIP},
		}
		clusterMachines.EXPECT().GetMachinesByCluster(clusterName).Return(expected, nil)

		nodes, err := clusterManager.GetClusterNodesInfo(clusterName)

		require.NoError(t, err)
		assert.Equal(t, expected, nodes)
	})

	t.Run("error", func(t *testing.T) {
		_, _, clusterMachines, clusterManager := newTestClusterManager(t)
		clusterMachines.EXPECT().GetMachinesByCluster(clusterName).Return(nil, testError)

		nodes, err := clusterManager.GetClusterNodesInfo(clusterName)

		assert.Nil(t, nodes)
		assert.ErrorIs(t, err, testError)
	})
}

func TestGetClusterConfig(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		_, clusterConfigs, _, clusterManager := newTestClusterManager(t)
		clusterConfigs.EXPECT().Get(clusterName).Return(clusterConfig, nil)

		config, err := clusterManager.GetClusterConfig(clusterName)

		require.NoError(t, err)
		assert.Equal(t, clusterConfig, config)
	})

	t.Run("error", func(t *testing.T) {
		_, clusterConfigs, _, clusterManager := newTestClusterManager(t)
		clusterConfigs.EXPECT().Get(clusterName).Return("", testError)

		config, err := clusterManager.GetClusterConfig(clusterName)

		assert.Empty(t, config)
		assert.ErrorIs(t, err, testError)
	})
}

func TestDeleteCluster(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().Delete(clusterName).Return(nil)

		err := clusterManager.DeleteCluster(clusterName)

		require.NoError(t, err)
	})

	t.Run("error", func(t *testing.T) {
		clusterResources, _, _, clusterManager := newTestClusterManager(t)
		clusterResources.EXPECT().Delete(clusterName).Return(testError)

		err := clusterManager.DeleteCluster(clusterName)

		assert.ErrorIs(t, err, testError)
	})
}

func newTestClusterManager(t *testing.T) (
	*mocks.MockForManagingClusterResources,
	*mocks.MockForGettingClusterConfig,
	*mocks.MockForGettingClusterMachines,
	cluster_manager.ClusterManager,
) {
	t.Helper()

	ctrl := gomock.NewController(t)
	clusterResources := mocks.NewMockForManagingClusterResources(ctrl)
	clusterConfigs := mocks.NewMockForGettingClusterConfig(ctrl)
	clusterMachines := mocks.NewMockForGettingClusterMachines(ctrl)
	clusterManager := cluster_manager.NewClusterManager(clusterResources, clusterConfigs, clusterMachines)

	return clusterResources, clusterConfigs, clusterMachines, clusterManager
}
