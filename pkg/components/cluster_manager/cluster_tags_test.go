package cluster_manager_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"capv-proxy/pkg/components/cluster_manager"
	"capv-proxy/pkg/strategies"
	"capv-proxy/pkg/strategies/mocks"
)

func TestClusterManagerTags(t *testing.T) {
	ctrl := gomock.NewController(t)
	resources := mocks.NewMockForManagingClusterResources(ctrl)
	manager := cluster_manager.NewClusterManager(resources, nil, nil, nil)

	resources.EXPECT().GetTags(t.Context(), "cluster").Return(map[string]string{"owner": "platform"}, nil)
	tags, err := manager.GetClusterTags(t.Context(), "cluster")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"owner": "platform"}, tags)

	resources.EXPECT().GetTag(t.Context(), "cluster", "owner").Return("platform", nil)
	value, err := manager.GetClusterTag(t.Context(), "cluster", "owner")
	require.NoError(t, err)
	assert.Equal(t, "platform", value)

	resources.EXPECT().SetTag(t.Context(), "cluster", "owner", "compute").Return(nil)
	require.NoError(t, manager.SetClusterTag(t.Context(), "cluster", "owner", "compute"))

	resources.EXPECT().DeleteTag(t.Context(), "cluster", "owner").Return(nil)
	require.NoError(t, manager.DeleteClusterTag(t.Context(), "cluster", "owner"))
}

func TestClusterManagerTagsWrapsResourceErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	resources := mocks.NewMockForManagingClusterResources(ctrl)
	manager := cluster_manager.NewClusterManager(resources, nil, nil, nil)
	errTest := errors.New("test error")

	resources.EXPECT().SetTag(t.Context(), "cluster", "owner", "platform").Return(errTest)
	err := manager.SetClusterTag(t.Context(), "cluster", "owner", "platform")
	assert.ErrorIs(t, err, errTest)
	assert.ErrorIs(t, err, strategies.ErrClusterTagUpdateFailed)
}
