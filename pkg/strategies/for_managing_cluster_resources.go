package strategies

import "context"

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_cluster_resources.go -destination=mocks/for_managing_cluster_resources.go -package=mocks
type ForManagingClusterResources interface {
	Create(ctx context.Context, params CreateClusterResourceParams) (ClusterInfo, error)
	List(ctx context.Context) ([]ClusterInfo, error)
	Get(ctx context.Context, name string) (ClusterInfo, error)
	Delete(ctx context.Context, name string) error
}
