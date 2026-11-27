// Package flavor holds the behavior that depends on how a cluster's control
// plane is managed, selected from Cluster.spec.controlPlaneRef. Everything
// else (Cluster, Machines, tags, pause, locks) is plain Cluster API and stays
// in the management client, which reaches flavors through For.
//
// To support a new control plane kind, add a flavor type named after the kind
// in its own file and register it in flavors. When the provider follows the
// Cluster API control plane contract, embed contractFlavor (contract.go) and
// override only what the engine does beyond it (vates_kube_os.go adds nothing,
// kubeadm.go adds upgrades). Otherwise write the flavor from scratch
// (rancher.go), as when another controller owns and re-renders the CAPI objects.
package flavor

import (
	"context"
	"fmt"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vatesfr/vmk-engine/pkg/strategies"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	cpGroup                 = "controlplane.cluster.x-k8s.io"
	controlPlaneKind        = "KubeadmControlPlane"
	specKey                 = "spec"
	replicasKey             = "replicas"
	nameKey                 = "name"
	kubeadmConfigSpecKey    = "kubeadmConfigSpec"
	clusterConfigurationKey = "clusterConfiguration"
)

// Flavor is the kind-dependent part of cluster management. A flavor refuses
// what its kind does not support by returning a scaling or upgrade conflict;
// callers never check the kind themselves.
type Flavor interface {
	// AddInfo fills what the CAPI Cluster does not carry for this kind.
	AddInfo(ctx context.Context, cluster *capiv1.Cluster, info *strategies.ClusterInfo) error
	SetControlPlaneReplicas(ctx context.Context, cluster *capiv1.Cluster, replicas int32) error
	SetWorkerReplicas(ctx context.Context, cluster *capiv1.Cluster, replicas int32) error
	Delete(ctx context.Context, cluster *capiv1.Cluster) error
	// UpgradeControlPlane returns the control plane object the upgrade patches.
	UpgradeControlPlane(ctx context.Context, cluster *capiv1.Cluster) (*unstructured.Unstructured, error)
}

// Host is what flavors use from the management client: its Kubernetes access
// and the shared Cluster API operations and checks they build on.
type Host interface {
	Client() client.Client
	Cache() client.Reader
	Namespace() string
	ValidateScalingObject(obj client.Object, cluster *capiv1.Cluster) error
	ValidateUpgradeMembership(obj client.Object, cluster *capiv1.Cluster) error
	PatchReplicas(ctx context.Context, obj, before client.Object) error
	ScaleWorkerDeployment(ctx context.Context, cluster *capiv1.Cluster, replicas int32) error
	DeleteCAPICluster(ctx context.Context, cluster *capiv1.Cluster) error
	MachineVersion(machine *capiv1.Machine) string
	MutationWriteError(err error) error
}

type entry struct {
	// capabilities are static so they can be reported without any read,
	// including in cluster events. They must match what the flavor accepts.
	capabilities strategies.ClusterCapabilities
	build        func(Host) Flavor
}

var flavors = map[schema.GroupKind]entry{
	kubeadmContract.gvk.GroupKind(): {
		capabilities: strategies.ClusterCapabilities{ScaleControlPlane: true, ScaleWorkers: true, Upgrade: true},
		build:        func(h Host) Flavor { return kubeadmFlavor{contractFlavor{h, kubeadmContract}} },
	},
	vatesKubeOSContract.gvk.GroupKind(): {
		capabilities: strategies.ClusterCapabilities{ScaleControlPlane: true, ScaleWorkers: true},
		build:        func(h Host) Flavor { return vatesKubeOSFlavor{contractFlavor{h, vatesKubeOSContract}} },
	},
	{Group: RancherControlPlaneGroup, Kind: RancherControlPlaneKind}: {
		capabilities: strategies.ClusterCapabilities{ScaleControlPlane: true, ScaleWorkers: true},
		build:        func(h Host) Flavor { return rancherFlavor{h} },
	},
}

// For returns the flavor serving cluster; unknown kinds get unsupportedFlavor.
func For(host Host, cluster *capiv1.Cluster) Flavor {
	if entry, found := flavors[groupKind(cluster)]; found {
		return entry.build(host)
	}
	return unsupportedFlavor{host}
}

// Capabilities reports what the engine supports for cluster's kind.
func Capabilities(cluster *capiv1.Cluster) strategies.ClusterCapabilities {
	return flavors[groupKind(cluster)].capabilities
}

func groupKind(cluster *capiv1.Cluster) schema.GroupKind {
	ref := cluster.Spec.ControlPlaneRef
	return schema.GroupKind{Group: ref.APIGroup, Kind: ref.Kind}
}

// unsupportedFlavor serves control plane kinds the engine does not know:
// clusters are listed and can be deleted, but are never resized, since their
// owner may re-render or reject the change.
type unsupportedFlavor struct {
	Host
}

func (unsupportedFlavor) AddInfo(context.Context, *capiv1.Cluster, *strategies.ClusterInfo) error {
	return nil
}

func (unsupportedFlavor) SetControlPlaneReplicas(_ context.Context, cluster *capiv1.Cluster, _ int32) error {
	return unsupportedKindConflict(cluster)
}

func (unsupportedFlavor) SetWorkerReplicas(_ context.Context, cluster *capiv1.Cluster, _ int32) error {
	return unsupportedKindConflict(cluster)
}

func (f unsupportedFlavor) Delete(ctx context.Context, cluster *capiv1.Cluster) error {
	return f.DeleteCAPICluster(ctx, cluster)
}

func (unsupportedFlavor) UpgradeControlPlane(_ context.Context, cluster *capiv1.Cluster) (*unstructured.Unstructured, error) {
	return nil, unsupportedUpgradeConflict(cluster)
}

func scalingConflict(reason string) error {
	return fmt.Errorf("%w: %s", strategies.ErrClusterScalingConflict, reason)
}

func upgradeConflict(reason string) error {
	return fmt.Errorf("%w: %s", strategies.ErrClusterUpgradeConflict, reason)
}

func unsupportedKindConflict(cluster *capiv1.Cluster) error {
	return scalingConflict(fmt.Sprintf("control plane kind %q is not supported", groupKind(cluster)))
}

func unsupportedUpgradeConflict(cluster *capiv1.Cluster) error {
	return upgradeConflict(fmt.Sprintf("control plane kind %q cannot be upgraded by the engine", groupKind(cluster)))
}

// machineVersions counts machines per version, in first-seen order.
func machineVersions(h Host, machines []capiv1.Machine) []strategies.VersionNodeStatus {
	counts := map[string]int{}
	var order []string
	for i := range machines {
		version := h.MachineVersion(&machines[i])
		if counts[version] == 0 {
			order = append(order, version)
		}
		counts[version]++
	}
	var result []strategies.VersionNodeStatus
	for _, version := range order {
		result = append(result, strategies.VersionNodeStatus{Replicas: counts[version], Version: version})
	}
	return result
}
