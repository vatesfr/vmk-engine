package handlers

import (
	"github.com/go-fuego/fuego"
	"github.com/samber/lo"

	"capv-proxy/pkg/strategies"
)

type VersionsHandler struct {
	versionService strategies.ForManagingVersions
}

type VersionGroup struct {
	Version  string   `json:"version"`
	Releases []string `json:"releases"`
}

func NewVersionsHandler(versionService strategies.ForManagingVersions) VersionsHandler {
	return VersionsHandler{versionService: versionService}
}

func (v VersionsHandler) GetAvailableVersion(c fuego.ContextNoBody) ([]VersionGroup, error) {
	versions, err := v.versionService.GetAllVersions(c.Context())
	if err != nil {
		return nil, toHTTPError(err)
	}
	return toVersionGroups(versions), nil
}

func toVersionGroups(versions []strategies.VersionGroup) []VersionGroup {
	return lo.Map(versions, func(version strategies.VersionGroup, _ int) VersionGroup {
		return VersionGroup{
			Version:  version.Version,
			Releases: version.Releases,
		}
	})
}
