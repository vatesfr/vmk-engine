package cluster_manager

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	"capv-proxy/pkg/strategies"
)

func (c ClusterManager) SetWorkerReplicas(ctx context.Context, id uuid.UUID, replicas int32) error {
	if err := validateScalingParams(id, replicas); err != nil {
		return err
	}
	if err := c.clusterResources.SetWorkerReplicas(ctx, id, replicas); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterScalingFailed, err)
	}
	return nil
}

func (c ClusterManager) SetControlPlaneReplicas(ctx context.Context, id uuid.UUID, replicas int32) error {
	if err := validateScalingParams(id, replicas); err != nil {
		return err
	}
	if err := c.clusterResources.SetControlPlaneReplicas(ctx, id, replicas); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterScalingFailed, err)
	}
	return nil
}

func validateScalingParams(id uuid.UUID, replicas int32) error {
	if err := validateClusterUUID(id); err != nil {
		return err
	}
	if replicas < 1 {
		return strategies.ErrInvalidReplicas
	}
	return nil
}
