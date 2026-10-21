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
	GetUpgradeContext(ctx context.Context, name string) (UpgradeContext, error)
	GetUpgradeControlPlane(ctx context.Context, cluster ResourceSnapshot) (UpgradeResourceState, error)
	GetUpgradeWorkers(ctx context.Context, cluster ResourceSnapshot) ([]UpgradeWorkerState, error)
	GetUpgradeMachines(ctx context.Context, cluster ResourceSnapshot) ([]UpgradeMachineState, error)
	ListActiveUpgrades(ctx context.Context) ([]string, error)
	BuildControlPlaneUpgradeTarget(ctx context.Context, cluster, controlPlane ResourceSnapshot, version string) (UpgradeTarget, error)
	BuildWorkerUpgradeTarget(ctx context.Context, cluster, worker ResourceSnapshot, version string) (UpgradeTarget, error)
	SaveUpgrade(ctx context.Context, cluster ResourceSnapshot, operation UpgradeOperation) error
	ApplyControlPlaneUpgrade(ctx context.Context, cluster ResourceSnapshot, version string, target UpgradeTarget) error
	ApplyWorkerUpgrade(ctx context.Context, cluster ResourceSnapshot, operationID, version string, targets []UpgradeTarget) error
	IsControlPlaneUpgradeReady(ctx context.Context, cluster ResourceSnapshot, version string, target UpgradeTarget) (bool, error)
	IsWorkerUpgradeReady(ctx context.Context, cluster ResourceSnapshot, operationID, version string, targets []UpgradeTarget) (bool, error)
	GetUpgrade(ctx context.Context, name string) (UpgradeOperation, error)
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
