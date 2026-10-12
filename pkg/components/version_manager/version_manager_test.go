package versionmanager_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	versionmanager "capv-proxy/pkg/components/version_manager"
)

func TestVersionCatalogue(t *testing.T) {
	manager := versionmanager.NewVersionManager()
	versions, err := manager.GetAllVersions(context.Background())
	require.NoError(t, err)
	require.Len(t, versions, 3)
	assert.Equal(t, "1.37", versions[0].Version)
	assert.Equal(t, "1.36", versions[1].Version)
	assert.Equal(t, "1.35", versions[2].Version)
	for _, group := range versions {
		for _, release := range group.Releases {
			assert.True(t, manager.IsVersionAvailable(release), release)
		}
	}
	assert.False(t, manager.IsVersionAvailable("1.37"))
	assert.False(t, manager.IsVersionAvailable("1.37.2"))
	assert.False(t, manager.IsVersionAvailable(""))
	assert.False(t, manager.IsVersionAvailable("v1.36.9"))

	versions[0].Releases[0] = "modified"
	fresh, err := manager.GetAllVersions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "1.37.1", fresh[0].Releases[0])
}

func TestGetAllVersionsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	versions, err := versionmanager.NewVersionManager().GetAllVersions(ctx)
	assert.Nil(t, versions)
	assert.ErrorIs(t, err, context.Canceled)
}
