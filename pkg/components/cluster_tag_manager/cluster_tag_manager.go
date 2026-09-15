package cluster_tag_manager

import (
	"context"
	"fmt"

	"capv-proxy/pkg/strategies"
)

type ClusterTagManager struct {
	clusterTags strategies.ForManagingClusterTagResources
}

var _ strategies.ForManagingClusterTags = ClusterTagManager{}

func NewClusterTagManager(clusterTags strategies.ForManagingClusterTagResources) ClusterTagManager {
	return ClusterTagManager{clusterTags: clusterTags}
}

func (m ClusterTagManager) GetClusterTags(ctx context.Context, clusterName string) (map[string]string, error) {
	tags, err := m.clusterTags.GetTags(ctx, clusterName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrClusterTagsRetrievalFailed, err)
	}
	return tags, nil
}

func (m ClusterTagManager) GetClusterTag(ctx context.Context, clusterName, tag string) (string, error) {
	value, err := m.clusterTags.GetTag(ctx, clusterName, tag)
	if err != nil {
		return "", fmt.Errorf("%w: %w", strategies.ErrClusterTagsRetrievalFailed, err)
	}
	return value, nil
}

func (m ClusterTagManager) SetClusterTag(ctx context.Context, clusterName, tag, value string) error {
	if err := m.clusterTags.SetTag(ctx, clusterName, tag, value); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterTagUpdateFailed, err)
	}
	return nil
}

func (m ClusterTagManager) DeleteClusterTag(ctx context.Context, clusterName, tag string) error {
	if err := m.clusterTags.DeleteTag(ctx, clusterName, tag); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterTagDeletionFailed, err)
	}
	return nil
}
