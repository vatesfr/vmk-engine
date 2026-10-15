package strategies

import (
	"context"

	"github.com/gofrs/uuid"
)

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_cluster_machines.go -destination=mocks/for_managing_cluster_machines.go -package=mocks
type ForManagingClusterMachines interface {
	GetNodeName(ctx context.Context, clusterID, machineID uuid.UUID) (string, error)
	GetMachineByUUID(ctx context.Context, id uuid.UUID) (ClusterNodeInfo, error)
	SetMachinePaused(ctx context.Context, clusterID, machineID uuid.UUID, paused bool) error
	GetMachinesByCluster(ctx context.Context, clusterID uuid.UUID) ([]ClusterNodeInfo, error)
	GetAllMachines(ctx context.Context) ([]ClusterNodeInfo, error)
	DeleteMachine(ctx context.Context, clusterID uuid.UUID, machineID uuid.UUID) error
}
