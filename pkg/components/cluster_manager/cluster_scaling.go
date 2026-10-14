package cluster_manager

import (
	"context"
	"fmt"

	"capv-proxy/pkg/strategies"
)

func (c ClusterManager) SetWorkerReplicas(ctx context.Context, name string, replicas int32) error {
	if err := validateScalingParams(name, replicas); err != nil {
		return err
	}
	if err := c.clusterResources.SetWorkerReplicas(ctx, name, replicas); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterScalingFailed, err)
	}
	return nil
}

func (c ClusterManager) SetControlPlaneReplicas(ctx context.Context, name string, replicas int32) error {
	if err := validateScalingParams(name, replicas); err != nil {
		return err
	}
	if err := c.clusterResources.SetControlPlaneReplicas(ctx, name, replicas); err != nil {
		return fmt.Errorf("%w: %w", strategies.ErrClusterScalingFailed, err)
	}
	return nil
}

func validateScalingParams(name string, replicas int32) error {
	if err := validateClusterName(name); err != nil {
		return err
	}
	if replicas < 1 {
		return strategies.ErrInvalidReplicas
	}
	return nil
}
