package strategies

import "context"

type ForManagingClusterTags interface {
	GetClusterTags(ctx context.Context, clusterID string) map[string]string
	GetClusterTag(ctx context.Context, clusterID, tag string) string
	SetClusterTag(ctx context.Context, clusterID, tag, value string)
	DeleteClusterTag(ctx context.Context, clusterID, tag string)
}
