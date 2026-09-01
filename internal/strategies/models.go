package strategies

// CreateClusterParams describes the minimal cluster creation intent.
type CreateClusterParams struct {
	Name                 string
	ControlPlaneReplicas int32
	WorkerReplicas       int32
	ControlPlaneIP       string
	VMNamePrefix         string
}

// CreateClusterResourceParams describes the complete topology required by the
// cluster resource Strategy.
type CreateClusterResourceParams struct {
	Name                 string
	ClusterClassName     string
	KubernetesVersion    string
	ControlPlaneReplicas *int32
	MachineDeployments   []MachineDeployment
	Variables            map[string]any
}

type MachineDeployment struct {
	Name          string
	Class         string
	Replicas      *int32
	FailureDomain string
}

// ClusterInfo is the shared representation returned by cluster Strategies.
type ClusterInfo struct {
	Name                 string
	Phase                string
	ControlPlaneEndpoint string
}
