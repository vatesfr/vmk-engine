package handlers_test

import (
	"testing"

	"github.com/go-fuego/fuego"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/vatesfr/vmk-engine/pkg/components/webserver/handlers"
	"github.com/vatesfr/vmk-engine/pkg/strategies"
	"github.com/vatesfr/vmk-engine/pkg/strategies/mocks"
)

func TestGetAvailableVersion(t *testing.T) {
	t.Run("preserves service order and maps response", func(t *testing.T) {
		service := mocks.NewMockForManagingVersions(gomock.NewController(t))
		service.EXPECT().GetAllVersions(gomock.Any()).Return([]strategies.VersionGroup{
			{Version: "1.37", Releases: []string{"1.37.1"}},
			{Version: "1.36", Releases: []string{clusterVersion, "1.36.8"}},
		}, nil)
		result, err := handlers.NewVersionsHandler(service).GetAvailableVersion(fuego.NewMockContextNoBody())
		assert.NoError(t, err)
		assert.Equal(t, []handlers.VersionGroup{
			{Version: "1.37", Releases: []string{"1.37.1"}},
			{Version: "1.36", Releases: []string{clusterVersion, "1.36.8"}},
		}, result)
	})
	t.Run("empty catalog returns an empty array", func(t *testing.T) {
		service := mocks.NewMockForManagingVersions(gomock.NewController(t))
		service.EXPECT().GetAllVersions(gomock.Any()).Return(nil, nil)
		result, err := handlers.NewVersionsHandler(service).GetAvailableVersion(fuego.NewMockContextNoBody())
		assert.NoError(t, err)
		assert.Equal(t, []handlers.VersionGroup{}, result)
	})
	t.Run("service error", func(t *testing.T) {
		service := mocks.NewMockForManagingVersions(gomock.NewController(t))
		service.EXPECT().GetAllVersions(gomock.Any()).Return(nil, errTest)
		result, err := handlers.NewVersionsHandler(service).GetAvailableVersion(fuego.NewMockContextNoBody())
		assert.Nil(t, result)
		assertHandlerError(t, err, errTest)
	})
}
