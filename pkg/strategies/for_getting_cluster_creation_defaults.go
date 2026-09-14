package strategies

import "context"

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_getting_cluster_creation_defaults.go -destination=mocks/for_getting_cluster_creation_defaults.go -package=mocks
type ForGettingClusterCreationDefaults interface {
	GetClusterCreationDefaults(ctx context.Context) (ClusterCreationDefaults, error)
}
