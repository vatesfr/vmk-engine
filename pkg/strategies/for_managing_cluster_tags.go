package strategies

import "context"

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_cluster_tags.go -destination=mocks/for_managing_cluster_tags.go -package=mocks
type ForManagingClusterTags interface {
	GetClusterTags(ctx context.Context, clusterID string) (map[string]string, error)
	GetClusterTag(ctx context.Context, clusterID, tag string) (string, error)
	SetClusterTag(ctx context.Context, clusterID, tag, value string) error
	DeleteClusterTag(ctx context.Context, clusterID, tag string) error
}
