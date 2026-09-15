package strategies

import "context"

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_cluster_tag_resources.go -destination=mocks/for_managing_cluster_tag_resources.go -package=mocks
type ForManagingClusterTagResources interface {
	GetTags(ctx context.Context, clusterName string) (map[string]string, error)
	GetTag(ctx context.Context, clusterName, tag string) (string, error)
	SetTag(ctx context.Context, clusterName, tag, value string) error
	DeleteTag(ctx context.Context, clusterName, tag string) error
}
