package cluster_manager

import (
	"context"

	"github.com/gofrs/uuid"

	"capv-proxy/pkg/strategies"
)

func (c ClusterManager) resolveNode(ctx context.Context, clusterID, nodeID uuid.UUID) (string, string, error) {
	if err := validateClusterUUID(clusterID); err != nil {
		return "", "", err
	}
	if nodeID == uuid.Nil {
		return "", "", strategies.ErrNodeIDRequired
	}
	cluster, err := c.clusterResources.GetByUUID(ctx, clusterID)
	if err != nil {
		return "", "", err
	}
	nodeName, err := c.clusterMachines.GetNodeName(ctx, clusterID, nodeID)
	return cluster.Name, nodeName, err
}

func (c ClusterManager) SetClusterNodeCordon(ctx context.Context, clusterID, nodeID uuid.UUID, cordoned bool) error {
	clusterName, nodeName, err := c.resolveNode(ctx, clusterID, nodeID)
	if err != nil {
		return err
	}
	if c.workloadNodes == nil {
		return strategies.ErrWorkloadUnavailable
	}
	return c.workloadNodes.SetCordon(ctx, clusterName, nodeName, cordoned)
}

func (c ClusterManager) DrainClusterNode(ctx context.Context, clusterID, nodeID uuid.UUID, options strategies.DrainOptions) error {
	if options.Timeout < 0 || options.Timeout > strategies.MaxDrainTimeout {
		return strategies.ErrInvalidDrainOptions
	}
	clusterName, nodeName, err := c.resolveNode(ctx, clusterID, nodeID)
	if err != nil {
		return err
	}
	if c.workloadNodes == nil {
		return strategies.ErrWorkloadUnavailable
	}
	return c.workloadNodes.Drain(ctx, clusterName, nodeName, options)
}

func (c ClusterManager) getNodeStates(ctx context.Context, clusterID uuid.UUID) map[string]strategies.WorkloadNodeState {
	cluster, err := c.clusterResources.GetByUUID(ctx, clusterID)
	if err != nil {
		return nil
	}
	states, _ := c.workloadStates.GetNodeStates(ctx, cluster.Name)
	return states
}

// Enrichment is best effort: management information remains available during outages.
func (c ClusterManager) enrichNodes(ctx context.Context, nodes []strategies.ClusterNodeInfo) {
	if c.workloadStates == nil {
		return
	}
	states := make(map[uuid.UUID]map[string]strategies.WorkloadNodeState)
	for i := range nodes {
		clusterName := nodes[i].ParentCluster
		if _, fetched := states[clusterName]; !fetched {
			// A partial result can still contain known cordon states after a pod-list failure.
			states[clusterName] = c.getNodeStates(ctx, clusterName)
		}
		state := states[clusterName][nodes[i].NodeName]
		nodes[i].IsCordon, nodes[i].IsDraining = state.IsCordon, state.IsDraining
	}
}

func (c ClusterManager) enrichCluster(ctx context.Context, cluster *strategies.ClusterInfo) {
	if c.workloadStates == nil {
		return
	}
	states, _ := c.workloadStates.GetNodeStates(ctx, cluster.Name)
	if len(states) == 0 {
		return
	}
	cordon, draining := make([]*bool, 0, len(states)), make([]*bool, 0, len(states))
	for _, state := range states {
		cordon = append(cordon, state.IsCordon)
		draining = append(draining, state.IsDraining)
	}
	// Missing Machine node references must not turn an unknown state into false.
	unknown := false
	if c.clusterMachines != nil {
		machines, err := c.clusterMachines.GetMachinesByCluster(ctx, cluster.ID)
		unknown = err != nil
		for _, machine := range machines {
			if _, found := states[machine.NodeName]; !found {
				unknown = true
			}
		}
	}
	cluster.IsCordon = aggregateState(cordon, unknown)
	cluster.IsDraining = aggregateState(draining, unknown)
}

func aggregateState(states []*bool, unknown bool) *bool {
	for _, state := range states {
		if state == nil {
			unknown = true
		} else if *state {
			value := true
			return &value
		}
	}
	if unknown {
		return nil
	}
	value := false
	return &value
}
