package strategies

import "context"

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_getting_workload_node_states.go -destination=mocks/for_getting_workload_node_states.go -package=mocks
type ForGettingWorkloadNodeStates interface {
	GetNodeStates(ctx context.Context, clusterName string) (map[string]WorkloadNodeState, error)
}

type WorkloadNodeState struct {
	IsCordon   *bool
	IsDraining *bool
}
