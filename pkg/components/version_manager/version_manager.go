package versionmanager

import (
	"context"
	"slices"

	"github.com/vatesfr/vmk-engine/pkg/strategies"
)

type VersionManager struct{}

var _ strategies.ForManagingVersions = VersionManager{}

func NewVersionManager() VersionManager {
	return VersionManager{}
}

// GetAllVersions returns minor versions and releases from newest to oldest.
func (v VersionManager) GetAllVersions(ctx context.Context) ([]strategies.VersionGroup, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return availableVersions(), nil
}

func (v VersionManager) IsVersionAvailable(version string) bool {
	for _, group := range availableVersions() {
		if slices.Contains(group.Releases, version) {
			return true
		}
	}
	return false
}

func availableVersions() []strategies.VersionGroup {
	return []strategies.VersionGroup{
		{Version: "1.37", Releases: []string{"1.37.1"}},
		{Version: "1.36", Releases: []string{"1.36.5", "1.36.4", "1.36.3", "1.36.2", "1.36.1"}},
		{Version: "1.35", Releases: []string{"1.35.9", "1.35.8", "1.35.7", "1.35.6", "1.35.5", "1.35.4", "1.35.3", "1.35.2", "1.35.1"}},
	}
}
