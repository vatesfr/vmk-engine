package cluster_defaults

import (
	"context"

	"capv-proxy/pkg/strategies"
)

type ClusterDefaults struct {
	defaults strategies.ClusterCreationDefaults
}

var _ strategies.ForGettingClusterCreationDefaults = ClusterDefaults{}

func NewClusterDefaults() ClusterDefaults {
	return ClusterDefaults{
		defaults: strategies.ClusterCreationDefaults{
			ClusterClassName:           "vates-rhel-from-scratch",
			KubernetesVersion:          "v1.36.3",
			WorkerName:                 "worker-md-0",
			WorkerClass:                "worker-from-scratch",
			ControlPlaneEndpointPort:   6443,
			ControlPlaneEndpointSubnet: 16,
			ControlPlaneLoadBalancer:   "kube-vip",
		},
	}
}

func (c ClusterDefaults) GetClusterCreationDefaults(_ context.Context) (strategies.ClusterCreationDefaults, error) {
	return c.defaults, nil
}
