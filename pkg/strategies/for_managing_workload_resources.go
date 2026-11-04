package strategies

import (
	"context"
	"time"
)

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_managing_workload_resources.go -destination=mocks/for_managing_workload_resources.go -package=mocks
type ForManagingWorkloadResources interface {
	SetCordon(ctx context.Context, clusterName, nodeName string, cordoned bool) error
	Drain(ctx context.Context, clusterName, nodeName string, options DrainOptions) error
	GetNodeStates(ctx context.Context, clusterName string) (map[string]WorkloadNodeState, error)
	GetVolumes(ctx context.Context, clusterName string) ([]WorkloadVolume, error)
}

type DrainOptions struct {
	Timeout            time.Duration
	IgnoreDaemonSets   bool
	DeleteEmptyDirData bool
	Force              bool
}

const (
	DefaultDrainTimeout = 5 * time.Minute
	MaxDrainTimeout     = 30 * time.Minute
)

type WorkloadNodeState struct {
	IsCordon   *bool
	IsDraining *bool
}
