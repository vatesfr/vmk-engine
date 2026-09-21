package strategies

// CreateClusterParams describes the minimal cluster creation intent.
type CreateClusterParams struct {
	Name                 string
	ControlPlaneReplicas int32
	WorkerReplicas       int32
	ControlPlaneIP       string
	VMNamePrefix         string
	Tags                 map[string]string
}

// ClusterCreationDefaults contains the server-side topology values used to
// turn a cluster creation intent into a complete Cluster resource.
type ClusterCreationDefaults struct {
	ClusterClassName           string
	KubernetesVersion          string
	WorkerName                 string
	WorkerClass                string
	ControlPlaneEndpointPort   int
	ControlPlaneEndpointSubnet int
	ControlPlaneLoadBalancer   string
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
	Tags                 map[string]string
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
	CreatedAt            string
	Version              string
	ControlPlaneEndpoint string
	ControlPlanesStatus  NodeTypeStatus
	WorkersStatus        NodeTypeStatus
	Tags                 map[string]string
}

type NodeTypeStatus struct {
	AvailableReplicas int
	DesiredReplicas   int
	ReadyReplicas     int
	Replicas          int
}
