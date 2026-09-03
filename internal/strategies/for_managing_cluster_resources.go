package strategies

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_cluster_resources.go -destination=mocks/for_managing_cluster_resources.go -package=mocks
type ForManagingClusterResources interface {
	Create(CreateClusterResourceParams) (ClusterInfo, error)
	List() ([]ClusterInfo, error)
	Get(name string) (ClusterInfo, error)
	Delete(name string) error
}
