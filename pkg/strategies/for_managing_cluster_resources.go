package strategies

import (
	"context"

	"github.com/gofrs/uuid"
)

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_cluster_resources.go -destination=mocks/for_managing_cluster_resources.go -package=mocks
type ForManagingClusterResources interface {
	SetDescription(ctx context.Context, id uuid.UUID, description string) error
	SetWorkerReplicas(ctx context.Context, id uuid.UUID, replicas int32) error
	SetControlPlaneReplicas(ctx context.Context, id uuid.UUID, replicas int32) error
	SetPaused(ctx context.Context, id uuid.UUID, paused bool) error
	Create(ctx context.Context, params CreateClusterResourceParams) (ClusterInfo, error)
	List(ctx context.Context) ([]ClusterInfo, error)
	GetByUUID(ctx context.Context, id uuid.UUID) (ClusterInfo, error)
	Delete(ctx context.Context, id uuid.UUID) error
	GetAllTags(ctx context.Context) (map[string]map[string]string, error)
	GetTags(ctx context.Context, id uuid.UUID) (map[string]string, error)
	GetTag(ctx context.Context, id uuid.UUID, tag string) (string, error)
	SetTag(ctx context.Context, id uuid.UUID, tag, value string) error
	DeleteTag(ctx context.Context, id uuid.UUID, tag string) error
}
