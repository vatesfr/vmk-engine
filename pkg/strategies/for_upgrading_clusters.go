package strategies

import (
	"context"

	"github.com/gofrs/uuid"
)

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_upgrading_clusters.go -destination=mocks/for_upgrading_clusters.go -package=mocks
type ForUpgradingClusters interface {
	StartUpgrade(ctx context.Context, id uuid.UUID, version string) (UpgradeOperation, error)
}
