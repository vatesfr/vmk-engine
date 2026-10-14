package strategies

import (
	"context"

	"github.com/gofrs/uuid"
)

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_clusters.go -destination=mocks/for_managing_clusters.go -package=mocks
type ForManagingClusters interface {
	SetWorkerReplicas(ctx context.Context, name string, replicas int32) error
	SetControlPlaneReplicas(ctx context.Context, name string, replicas int32) error
	SetClusterPaused(ctx context.Context, name string, paused bool) error
	SetClusterNodePaused(ctx context.Context, clusterName, machineName string, paused bool) error
	CreateCluster(ctx context.Context, params CreateClusterParams) (ClusterInfo, error)
	GetClusters(ctx context.Context) ([]ClusterInfo, error)
	GetCluster(ctx context.Context, name string) (ClusterInfo, error)
	GetClusterNodesInfo(ctx context.Context, name string) ([]ClusterNodeInfo, error)
	GetClusterConfig(ctx context.Context, name string) (string, error)
	DeleteCluster(ctx context.Context, name string) error
	DeleteClusterNode(ctx context.Context, clusterName, nodeName string) error
	GetAllTags(ctx context.Context) (map[string]map[string]string, error)
	GetClusterTags(ctx context.Context, clusterID string) (map[string]string, error)
	GetClusterTag(ctx context.Context, clusterID, tag string) (string, error)
	SetClusterTag(ctx context.Context, clusterID, tag, value string) error
	DeleteClusterTag(ctx context.Context, clusterID, tag string) error
	GetAllNodes(ctx context.Context) ([]ClusterNodeInfo, error)
}

type ClusterNodeInfo struct {
	Paused        bool
	Name          string
	NodeName      string
	Role          string
	Status        string
	Endpoint      string
	MachineID     *uuid.UUID
	ParentCluster string
}
