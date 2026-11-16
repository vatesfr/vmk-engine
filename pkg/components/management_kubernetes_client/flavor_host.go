package management_kubernetes_client

import (
	"context"
	"log/slog"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vatesfr/vmk-engine/pkg/components/management_kubernetes_client/flavor"
	"github.com/vatesfr/vmk-engine/pkg/strategies"
)

// flavorHost gives cluster flavors the shared Cluster API operations they
// build on, without adding them to ClusterClient's exported API.
type flavorHost struct {
	ClusterClient
}

func (h flavorHost) Client() client.Client { return h.kubernetesClient }
func (h flavorHost) Cache() client.Reader  { return h.informerCache }
func (h flavorHost) Namespace() string     { return h.namespace }

func (flavorHost) ValidateScalingObject(obj client.Object, cluster *capiv1.Cluster) error {
	return validateScalingObject(obj, cluster)
}

func (flavorHost) ValidateUpgradeMembership(obj client.Object, cluster *capiv1.Cluster) error {
	return validateUpgradeMembership(obj, cluster)
}

func (h flavorHost) PatchReplicas(ctx context.Context, obj, before client.Object) error {
	return h.patchReplicas(ctx, obj, before)
}

func (h flavorHost) ScaleWorkerDeployment(ctx context.Context, cluster *capiv1.Cluster, replicas int32) error {
	return h.scaleWorkerDeployment(ctx, cluster, replicas)
}

func (h flavorHost) DeleteCAPICluster(ctx context.Context, cluster *capiv1.Cluster) error {
	return h.deleteCAPICluster(ctx, cluster)
}

func (flavorHost) MachineVersion(machine *capiv1.Machine) string { return machineVersion(machine) }
func (flavorHost) MutationWriteError(err error) error            { return mutationWriteError(err) }

func (c ClusterClient) flavor(cluster *capiv1.Cluster) flavor.Flavor {
	return flavor.For(flavorHost{c}, cluster)
}

// clusterInfo is toInfo plus the data the flavor keeps outside the CAPI
// Cluster. A flavor read failure is logged and leaves those fields as toInfo
// set them, so one broken cluster never fails a whole listing.
func (c ClusterClient) clusterInfo(ctx context.Context, cluster *capiv1.Cluster) strategies.ClusterInfo {
	info := toInfo(cluster)
	if err := c.flavor(cluster).AddInfo(ctx, cluster, &info); err != nil {
		slog.WarnContext(ctx, "read cluster flavor data", "cluster", cluster.Name, "kind", info.ControlPlaneKind, "error", err)
	}
	return info
}
