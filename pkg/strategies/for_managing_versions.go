package strategies

import "context"

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_versions.go -destination=mocks/for_managing_versions.go -package=mocks
type ForManagingVersions interface {
	GetAllVersions(ctx context.Context) ([]VersionGroup, error)
	IsVersionAvailable(version string) bool
}

// VersionGroup describes a Kubernetes minor version and its available releases.
type VersionGroup struct {
	Version  string
	Releases []string
}
