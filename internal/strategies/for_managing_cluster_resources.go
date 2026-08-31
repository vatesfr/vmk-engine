package strategies

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_cluster_resources.go -destination=mock/for_managing_cluster_resources.go -package=mock

type ForManagingClusterResources interface {
	Create(CreateClusterResourceParams) (ClusterResourceInfo, error)
	List() ([]ClusterResourceInfo, error)
	Get(name string) (ClusterResourceInfo, error)
	Delete(name string) error
}

type CreateClusterResourceParams struct {
	Name string
}

type ClusterResourceInfo struct {
	Name  string
	Phase string
}
