package strategies

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_cluster_resources.go -destination=mock/for_managing_cluster_resources.go -package=mock

type ForManagingClusterResources interface {
	Create(CreateClusterParams) (ClusterInfo, error)
	List() ([]ClusterInfo, error)
	Get(name string) (ClusterInfo, error)
	Delete(name string) error
}
