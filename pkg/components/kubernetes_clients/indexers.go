package kubernetes_clients

import (
	"context"
	"fmt"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/uuids"
)

// RegisterIndexes registers the UUID field indexes on the informer cache for
// Cluster and Machine resources. Must be called before cache.Start().
func RegisterIndexes(ctx context.Context, c cache.Cache) error {
	if err := c.IndexField(ctx, &capiv1.Cluster{}, uuids.UUIDIndexKey, clusterUUIDIndexer); err != nil {
		return fmt.Errorf("register cluster UUID index: %w", err)
	}
	if err := c.IndexField(ctx, &capiv1.Machine{}, uuids.UUIDIndexKey, machineUUIDIndexer); err != nil {
		return fmt.Errorf("register machine UUID index: %w", err)
	}
	return nil
}

// clusterUUIDIndexer computes the UUID v5 for a Cluster object.
func clusterUUIDIndexer(obj client.Object) []string {
	return []string{uuids.ClusterID(obj.GetName()).String()}
}

// machineUUIDIndexer computes the UUID v5 for a Machine object.
// Name resolution must match toClusterNodeInfo: NodeRef.Name takes precedence
// over machine.Name.
func machineUUIDIndexer(obj client.Object) []string {
	machine := obj.(*capiv1.Machine)
	clusterName := machine.Labels[capiv1.ClusterNameLabel]
	name := machine.Name
	if machine.Status.NodeRef.IsDefined() {
		name = machine.Status.NodeRef.Name
	}
	return []string{uuids.NodeID(clusterName, name).String()}
}
