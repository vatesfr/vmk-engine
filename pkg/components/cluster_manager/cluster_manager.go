package cluster_manager

import (
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

func (c ClusterManager) CreateCluster(params strategies.CreateClusterParams) (strategies.ClusterInfo, error) {
	if params.Name == "" {
		return strategies.ClusterInfo{}, ErrClusterNameRequired
	}

	if params.ControlPlaneIP == "" {
		return strategies.ClusterInfo{}, ErrControlPlaneIPRequired
	}

	if params.ControlPlaneReplicas <= 0 {
		return strategies.ClusterInfo{}, ErrControlPlaneReplicasRequired
	}

	if params.WorkerReplicas <= 0 {
		return strategies.ClusterInfo{}, ErrWorkerReplicasRequired
	}

	clusterInfo, err := c.clusterResources.Create(strategies.CreateClusterResourceParams{
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
		return strategies.ClusterInfo{}, NewErrClusterCreationFailed(err)
	}

	return clusterInfo, nil

}

func (c ClusterManager) GetClusters() ([]strategies.ClusterInfo, error) {
	return c.clusterResources.List()
}

func (c ClusterManager) GetCluster(name string) (strategies.ClusterInfo, error) {
	return c.clusterResources.Get(name)
}

func (c ClusterManager) GetClusterNodesInfo(name string) ([]strategies.ClusterNodeInfo, error) {
	return c.clusterMachines.GetMachinesByCluster(name)
}

func (c ClusterManager) GetClusterConfig(name string) (string, error) {
	return c.clusterConfigs.Get(name)
}

func (c ClusterManager) DeleteCluster(name string) error {
	return c.clusterResources.Delete(name)
}
