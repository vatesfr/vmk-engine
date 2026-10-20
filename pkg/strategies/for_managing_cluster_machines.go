package strategies

import (
	"context"

	"github.com/gofrs/uuid"
)

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_cluster_machines.go -destination=mocks/for_managing_cluster_machines.go -package=mocks
type ForManagingClusterMachines interface {
	GetMachineByUUID(ctx context.Context, id uuid.UUID) (ClusterNodeInfo, error)
	SetMachinePaused(ctx context.Context, clusterName, machineName string, paused bool) error
	GetMachinesByCluster(ctx context.Context, clusterName string) ([]ClusterNodeInfo, error)
	GetAllMachines(ctx context.Context) ([]ClusterNodeInfo, error)
	DeleteMachine(ctx context.Context, clusterName, machineName string) error
}
