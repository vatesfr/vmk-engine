package cluster_manager

import (
	"fmt"

	"capv-proxy/internal/strategies"
)

type ClusterManager struct {
	clusterResources strategies.ForManagingClusterResources
	clusterConfigs   strategies.ForGettingClusterConfig
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

	created, err := c.clusterResources.Create(strategies.CreateClusterResourceParams{Name: params.Name})
	if err != nil {
		return strategies.ClusterInfo{}, err
	}
	return toClusterInfo(created), nil
}

func (c ClusterManager) GetClusters() ([]strategies.ClusterInfo, error) {
	clusters, err := c.clusterResources.List()
	if err != nil {
		return nil, err
	}

	result := make([]strategies.ClusterInfo, 0, len(clusters))
	for _, value := range clusters {
		result = append(result, toClusterInfo(value))
	}
	return result, nil
}

func (c ClusterManager) GetCluster(name string) (strategies.ClusterInfo, error) {
	found, err := c.clusterResources.Get(name)
	if err != nil {
		return strategies.ClusterInfo{}, err
	}
	return toClusterInfo(found), nil
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

func toClusterInfo(value strategies.ClusterResourceInfo) strategies.ClusterInfo {
	return strategies.ClusterInfo{Name: value.Name, Phase: value.Phase}
}
