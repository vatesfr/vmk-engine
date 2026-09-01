package cluster_manager

import (
	"fmt"

	"capv-proxy/internal/strategies"
)

type ClusterManager struct {
	clusterResources strategies.ForManagingClusterResources
	clusterConfigs   strategies.ForGettingClusterConfig
}

type clusterCreationDefaults struct {
	clusterClassName     string
	kubernetesVersion    string
	controlPlaneReplicas int32
	workerName           string
	workerClass          string
	workerReplicas       int32
}

var defaultClusterCreation = clusterCreationDefaults{
	clusterClassName:     "production-v1",
	kubernetesVersion:    "v1.34.1",
	controlPlaneReplicas: 3,
	workerName:           "default",
	workerClass:          "default-worker",
	workerReplicas:       3,
}

var _ strategies.ForManagingClusters = ClusterManager{}

func NewClusterManager(
	clusterResources strategies.ForManagingClusterResources,
	clusterConfigs strategies.ForGettingClusterConfig,
) ClusterManager {
	return ClusterManager{
		clusterResources: clusterResources,
		clusterConfigs:   clusterConfigs,
	}
}

func (c ClusterManager) CreateCluster(params strategies.CreateClusterParams) (strategies.ClusterInfo, error) {
	if params.Name == "" {
		return strategies.ClusterInfo{}, fmt.Errorf("cluster name is required")
	}

	controlPlaneReplicas := defaultClusterCreation.controlPlaneReplicas
	workerReplicas := defaultClusterCreation.workerReplicas

	return c.clusterResources.Create(strategies.CreateClusterResourceParams{
		Name:                 params.Name,
		ClusterClassName:     defaultClusterCreation.clusterClassName,
		KubernetesVersion:    defaultClusterCreation.kubernetesVersion,
		ControlPlaneReplicas: &controlPlaneReplicas,
		MachineDeployments: []strategies.MachineDeployment{
			{
				Name:     defaultClusterCreation.workerName,
				Class:    defaultClusterCreation.workerClass,
				Replicas: &workerReplicas,
			},
		},
	})
}

func (c ClusterManager) GetClusters() ([]strategies.ClusterInfo, error) {
	return c.clusterResources.List()
}

func (c ClusterManager) GetCluster(name string) (strategies.ClusterInfo, error) {
	return c.clusterResources.Get(name)
}

func (c ClusterManager) GetClusterNodesInfo(name string) ([]strategies.ClusterNodeInfo, error) {
	return []strategies.ClusterNodeInfo{
		{Name: name + "-control-plane-0", Role: "control-plane", Status: "Ready", Endpoint: "https://192.0.2.10:10250"},
		{Name: name + "-worker-0", Role: "worker", Status: "Ready", Endpoint: "https://192.0.2.11:10250"},
	}, nil
}

func (c ClusterManager) GetClusterConfig(name string) (string, error) {
	return c.clusterConfigs.Get(name)
}

func (c ClusterManager) DeleteCluster(name string) error {
	return c.clusterResources.Delete(name)
}
