package strategies

import (
	"context"

	"github.com/gofrs/uuid"
)

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_clusters.go -destination=mocks/for_managing_clusters.go -package=mocks
type ForManagingClusters interface {
	SetClusterNodeCordon(ctx context.Context, clusterID, nodeID uuid.UUID, cordoned bool) error
	DrainClusterNode(ctx context.Context, clusterID, nodeID uuid.UUID, options DrainOptions) error
	SetClusterDescription(ctx context.Context, id uuid.UUID, description string) error
	SetWorkerReplicas(ctx context.Context, clusterID uuid.UUID, replicas int32) error
	SetControlPlaneReplicas(ctx context.Context, clusterID uuid.UUID, replicas int32) error
	SetClusterPaused(ctx context.Context, id uuid.UUID, paused bool) error
	SetClusterNodePaused(ctx context.Context, clusterID, nodeID uuid.UUID, paused bool) error
	CreateCluster(ctx context.Context, params CreateClusterParams) (ClusterInfo, error)
	GetClusters(ctx context.Context) ([]ClusterInfo, error)
	GetClusterByUUID(ctx context.Context, id uuid.UUID) (ClusterInfo, error)
	GetClusterNodesInfo(ctx context.Context, id uuid.UUID) ([]ClusterNodeInfo, error)
	GetClusterConfig(ctx context.Context, id uuid.UUID) (string, error)
	DeleteCluster(ctx context.Context, id uuid.UUID) error
	DeleteClusterNode(ctx context.Context, clusterID uuid.UUID, nodeID uuid.UUID) error
	GetAllTags(ctx context.Context) (map[string]map[string]string, error)
	GetClusterTags(ctx context.Context, id uuid.UUID) (map[string]string, error)
	GetClusterTag(ctx context.Context, id uuid.UUID, tag string) (string, error)
	SetClusterTag(ctx context.Context, id uuid.UUID, tag, value string) error
	DeleteClusterTag(ctx context.Context, id uuid.UUID, tag string) error
	GetAllNodes(ctx context.Context) ([]ClusterNodeInfo, error)
	GetNodeByUUID(ctx context.Context, id uuid.UUID) (ClusterNodeInfo, error)
}

type ClusterNodeInfo struct {
	ID            uuid.UUID
	IsCordon      *bool
	IsDraining    *bool
	Paused        bool
	Name          string
	NodeName      string
	Role          string
	Status        string
	Endpoint      string
	MachineID     *uuid.UUID
	ParentCluster uuid.UUID
	Version       string
}
