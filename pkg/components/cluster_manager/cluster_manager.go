package cluster_manager

import (
	"context"
	"fmt"

	"capv-proxy/pkg/strategies"
)

type ClusterManager struct {
	clusterResources strategies.ForManagingClusterResources
	clusterConfigs   strategies.ForGettingClusterConfig
	clusterMachines  strategies.ForGettingClusterMachines
}

type clusterCreationDefaults struct {
	clusterClassName           string
	kubernetesVersion          string
	workerName                 string
	workerClass                string
	controlPlaneEndpointPort   int
	controlPlaneEndpointSubnet int
	controlPlaneLoadBalancer   string
}

var defaultClusterCreation = clusterCreationDefaults{
	clusterClassName:           "vates-rhel-from-scratch",
	kubernetesVersion:          "v1.36.3",
	workerName:                 "worker-md-0",
	workerClass:                "worker-from-scratch",
	controlPlaneEndpointPort:   6443,
	controlPlaneEndpointSubnet: 16,
	controlPlaneLoadBalancer:   "kube-vip",
}

var _ strategies.ForManagingClusters = ClusterManager{}

func NewClusterManager(
	clusterResources strategies.ForManagingClusterResources,
	clusterConfigs strategies.ForGettingClusterConfig,
	clusterMachines strategies.ForGettingClusterMachines,
) ClusterManager {
	return ClusterManager{
		clusterResources: clusterResources,
		clusterConfigs:   clusterConfigs,
		clusterMachines:  clusterMachines,
	}
}

func (c ClusterManager) CreateCluster(ctx context.Context, params strategies.CreateClusterParams) (strategies.ClusterInfo, error) {
	if err := validateClusterName(params.Name); err != nil {
		return strategies.ClusterInfo{}, err
	}

	if params.ControlPlaneIP == "" {
		return strategies.ClusterInfo{}, strategies.ErrControlPlaneIPRequired
	}

	if params.ControlPlaneReplicas <= 0 {
		return strategies.ClusterInfo{}, strategies.ErrControlPlaneReplicasRequired
	}

	if params.WorkerReplicas <= 0 {
		return strategies.ClusterInfo{}, strategies.ErrWorkerReplicasRequired
	}

	clusterInfo, err := c.clusterResources.Create(ctx, strategies.CreateClusterResourceParams{
		Name:                 params.Name,
		ClusterClassName:     defaultClusterCreation.clusterClassName,
		KubernetesVersion:    defaultClusterCreation.kubernetesVersion,
		ControlPlaneReplicas: &params.ControlPlaneReplicas,
		MachineDeployments: []strategies.MachineDeployment{
			{
				Name:     defaultClusterCreation.workerName,
				Class:    defaultClusterCreation.workerClass,
				Replicas: &params.WorkerReplicas,
			},
		},
		Variables: map[string]any{
			"controlPlaneEndpoint": map[string]any{
				"host":   params.ControlPlaneIP,
				"port":   defaultClusterCreation.controlPlaneEndpointPort,
				"subnet": defaultClusterCreation.controlPlaneEndpointSubnet,
			},
			"vmNamePrefix":   params.VMNamePrefix,
			"controlPlaneLB": defaultClusterCreation.controlPlaneLoadBalancer,
		},
	})
	if err != nil {
		return strategies.ClusterInfo{}, fmt.Errorf("%w: %w", strategies.ErrClusterCreationFailed, err)
	}

	return clusterInfo, nil
}

func (c ClusterManager) GetClusters(ctx context.Context) ([]strategies.ClusterInfo, error) {
	clusters, err := c.clusterResources.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrClusterListingFailed, err)
	}
	return clusters, nil
}

func (c ClusterManager) GetCluster(ctx context.Context, name string) (strategies.ClusterInfo, error) {
	if err := validateClusterName(name); err != nil {
		return strategies.ClusterInfo{}, err
	}
	cluster, err := c.clusterResources.Get(ctx, name)
	if err != nil {
		return strategies.ClusterInfo{}, fmt.Errorf("%w: %w", strategies.ErrClusterRetrievalFailed, err)
	}
	return cluster, nil
}

func (c ClusterManager) GetClusterNodesInfo(ctx context.Context, name string) ([]strategies.ClusterNodeInfo, error) {
	if err := validateClusterName(name); err != nil {
		return nil, err
	}
	nodes, err := c.clusterMachines.GetMachinesByCluster(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrClusterNodesRetrievalFailed, err)
	}
	return nodes, nil
}

func (c ClusterManager) GetClusterConfig(ctx context.Context, name string) (string, error) {
	if err := validateClusterName(name); err != nil {
		return "", err
	}
	config, err := c.clusterConfigs.Get(ctx, name)
	if err != nil {
		return "", fmt.Errorf("%w: %w", strategies.ErrClusterConfigRetrievalFailed, err)
	}
	return config, nil
}

func (c ClusterManager) DeleteCluster(ctx context.Context, name string) error {
	if err := validateClusterName(name); err != nil {
		return err
	}
	if err := c.clusterResources.Delete(ctx, name); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterDeletionFailed, err)
	}
	return nil
}

func validateClusterName(name string) error {
	if name == "" {
		return strategies.ErrClusterNameRequired
	}
	return nil
}
