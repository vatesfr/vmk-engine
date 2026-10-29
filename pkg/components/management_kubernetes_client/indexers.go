package management_kubernetes_client

import (
	"context"
	"fmt"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	uuids "github.com/vatesfr/vmk-engine/pkg/helpers/uuids"
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
	if err := c.IndexField(ctx, &capiv1.Machine{}, uuids.ClusterUUIDIndexKey, machineClusterUUIDIndexer); err != nil {
		return fmt.Errorf("register machine cluster UUID index: %w", err)
	}
	return nil
}

// clusterUUIDIndexer computes the UUID v5 for a Cluster object.
func clusterUUIDIndexer(obj client.Object) []string {
	return []string{uuids.ClusterID(obj.GetName()).String()}
}

// machineUUIDIndexer computes the UUID v5 for a Machine object from
// spec.clusterName and metadata.name, matching toClusterNodeInfo. The Node name
// in status.nodeRef is deliberately not used: it is empty until the Node
// registers, and switching to it later would change the Machine's ID.
func machineUUIDIndexer(obj client.Object) []string {
	machine := obj.(*capiv1.Machine)
	return []string{uuids.NodeID(machine.Spec.ClusterName, machine.Name).String()}
}

// machineClusterUUIDIndexer computes the UUID v5 of the cluster a Machine
// belongs to using spec.clusterName, consistent with machineUUIDIndexer.
func machineClusterUUIDIndexer(obj client.Object) []string {
	machine := obj.(*capiv1.Machine)
	return []string{uuids.ClusterID(machine.Spec.ClusterName).String()}
}
