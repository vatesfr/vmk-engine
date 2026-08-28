package strategies

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_cluster.go -destination=mock/for_managing_cluster.go -package=mock
type ForManagingCluster interface {
	CreateCluster(params ClusterCreateParams) (ClusterInfo, error)
	GetClusters() ([]ClusterInfo, error)
	GetCluster(name string) (ClusterInfo, error)
	GetClusterNodesInfo(name string) ([]ClusterNodeInfo, error)
	GetClusterConfig(name string) (string, error)
	DeleteCluster(name string) error
}

type ClusterCreateParams struct {
	Name string `json:"name" validate:"required"`
}

type ClusterInfo struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
}

type ClusterNodeInfo struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Status   string `json:"status"`
	Endpoint string `json:"endpoint,omitempty"`
}
