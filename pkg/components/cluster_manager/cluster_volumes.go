package cluster_manager

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/vatesfr/vmk-engine/pkg/strategies"
)

func (c ClusterManager) GetClusterVolumes(ctx context.Context, id uuid.UUID) ([]strategies.WorkloadVolume, error) {
	if err := validateClusterUUID(id); err != nil {
		return nil, err
	}
	cluster, err := c.clusterResources.GetByUUID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", strategies.ErrClusterRetrievalFailed, err)
	}
	if c.workloadResources == nil {
		return nil, strategies.ErrWorkloadUnavailable
	}
	volumes, err := c.workloadResources.GetVolumes(ctx, cluster.Name)
	if err != nil {
		return nil, err
	}
	c.resolveVolumeNodes(ctx, id, volumes)
	return volumes, nil
}

// Enrichment is best effort: Volumes stays nil when the workload cluster cannot be read.
func (c ClusterManager) enrichClusterVolumes(ctx context.Context, cluster *strategies.ClusterInfo) {
	if c.workloadResources == nil {
		return
	}
	volumes, err := c.workloadResources.GetVolumes(ctx, cluster.Name)
	if err != nil {
		return
	}
	c.resolveVolumeNodes(ctx, cluster.ID, volumes)
	cluster.Volumes = volumes
}

// resolveVolumeNodes links attached Node names to the cluster's Machines.
// Unresolved Nodes keep only their name.
func (c ClusterManager) resolveVolumeNodes(ctx context.Context, clusterID uuid.UUID, volumes []strategies.WorkloadVolume) {
	if c.clusterMachines == nil {
		return
	}
	machines, err := c.clusterMachines.GetMachinesByCluster(ctx, clusterID)
	if err != nil {
		return
	}
	machinesByNode := make(map[string]strategies.ClusterNodeInfo, len(machines))
	for _, machine := range machines {
		if machine.NodeName != "" {
			machinesByNode[machine.NodeName] = machine
		}
	}
	for i := range volumes {
		for j := range volumes[i].AttachedNodes {
			node := &volumes[i].AttachedNodes[j]
			if machine, found := machinesByNode[node.NodeName]; found {
				node.ID, node.VMID = &machine.ID, machine.VMID
			}
		}
	}
}
