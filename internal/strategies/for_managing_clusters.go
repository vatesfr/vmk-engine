package strategies

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_clusters.go -destination=mock/for_managing_clusters.go -package=mock

type ForManagingClusters interface {
	CreateCluster(CreateClusterParams) (ClusterInfo, error)
	GetClusters() ([]ClusterInfo, error)
	GetCluster(name string) (ClusterInfo, error)
	GetClusterNodesInfo(name string) ([]ClusterNodeInfo, error)
	GetClusterConfig(name string) (string, error)
	DeleteCluster(name string) error
}

type CreateClusterParams struct {
	Name string
}

type ClusterInfo struct {
	Name  string
	Phase string
}

type ClusterNodeInfo struct {
	Name     string
	Role     string
	Status   string
	Endpoint string
}
