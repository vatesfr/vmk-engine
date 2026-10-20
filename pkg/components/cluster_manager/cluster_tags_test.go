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

const (
	ownerTag      = "owner"
	platformValue = "platform"
)

func TestClusterManagerGetAllTags(t *testing.T) {
	errTest := errors.New("test error")
	testCases := map[string]struct {
		tags map[string]map[string]string
		err  error
	}{
		"tags by cluster": {tags: map[string]map[string]string{"cluster": {ownerTag: platformValue}, "no-tags": {}}},
		"no clusters":     {tags: map[string]map[string]string{}},
		"resource error":  {err: errTest},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
			manager := cluster_manager.NewClusterManager(resources, nil, nil, nil, nil)
			resources.EXPECT().GetAllTags(t.Context()).Return(tc.tags, tc.err)

			tags, err := manager.GetAllTags(t.Context())

			assert.Equal(t, tc.tags, tags)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				assert.ErrorIs(t, err, strategies.ErrClusterTagsRetrievalFailed)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestClusterManagerTags(t *testing.T) {
	ctrl := gomock.NewController(t)
	resources := mocks.NewMockForManagingClusterResources(ctrl)
	manager := cluster_manager.NewClusterManager(resources, nil, nil, nil, nil)

	resources.EXPECT().GetTags(t.Context(), clusterUUID).Return(map[string]string{ownerTag: platformValue}, nil)
	tags, err := manager.GetClusterTags(t.Context(), clusterUUID)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{ownerTag: platformValue}, tags)

	resources.EXPECT().GetTag(t.Context(), clusterUUID, ownerTag).Return(platformValue, nil)
	value, err := manager.GetClusterTag(t.Context(), clusterUUID, ownerTag)
	require.NoError(t, err)
	assert.Equal(t, platformValue, value)

	resources.EXPECT().SetTag(t.Context(), clusterUUID, ownerTag, "compute").Return(nil)
	require.NoError(t, manager.SetClusterTag(t.Context(), clusterUUID, ownerTag, "compute"))

	resources.EXPECT().DeleteTag(t.Context(), clusterUUID, ownerTag).Return(nil)
	require.NoError(t, manager.DeleteClusterTag(t.Context(), clusterUUID, ownerTag))
}

func TestClusterManagerTagsWrapsResourceErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	resources := mocks.NewMockForManagingClusterResources(ctrl)
	manager := cluster_manager.NewClusterManager(resources, nil, nil, nil, nil)
	errTest := errors.New("test error")

	resources.EXPECT().SetTag(t.Context(), clusterUUID, ownerTag, platformValue).Return(errTest)
	err := manager.SetClusterTag(t.Context(), clusterUUID, ownerTag, platformValue)
	assert.ErrorIs(t, err, errTest)
	assert.ErrorIs(t, err, strategies.ErrClusterTagUpdateFailed)
}
