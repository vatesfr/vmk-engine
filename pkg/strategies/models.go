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

// ClusterCreationDefaults contains VM and bootstrap configuration read from the ConfigMap.
// Credentials remain in provider-managed Secrets.
type ClusterCreationDefaults struct {
	KubernetesVersion         string
	WorkerName                string
	PodCIDR                   string
	ServiceCIDR               string
	ServiceDomain             string
	ControlPlaneKubeadmConfig map[string]any
	ControlPlaneMachineSpec   map[string]any
	WorkerMachineSpec         map[string]any
	WorkerKubeadmConfig       map[string]any
	CNI                       string
	EndpointPort              int
	EndpointSubnet            int
	LoadBalancer              string
}

// CreateClusterResourceParams describes the resources required by the
// cluster resource Strategy.
type CreateClusterResourceParams struct {
	Defaults             ClusterCreationDefaults
	ControlPlaneIP       string
	VMNamePrefix         string
	Name                 string
	ControlPlaneReplicas *int32
	MachineDeployments   []MachineDeployment
	Tags                 map[string]string
}

type MachineDeployment struct {
	Name     string
	Replicas *int32
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
