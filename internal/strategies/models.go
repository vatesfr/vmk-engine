package strategies

// CreateClusterParams contains the transport-neutral parameters shared by the
// cluster-management and cluster-resource Strategies.
type CreateClusterParams struct {
	Name string
}

// ClusterInfo is the shared representation returned by cluster Strategies.
type ClusterInfo struct {
	Name  string
	Phase string
}
