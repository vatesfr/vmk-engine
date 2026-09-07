package strategies

import "context"

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_getting_cluster_config.go -destination=mocks/for_getting_cluster_config.go -package=mocks
type ForGettingClusterConfig interface {
	Get(ctx context.Context, clusterName string) (string, error)
}
