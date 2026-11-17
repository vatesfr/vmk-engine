package flavor

import (
	"context"
	"fmt"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	uuids "github.com/vatesfr/vmk-engine/pkg/helpers/uuids"
	"github.com/vatesfr/vmk-engine/pkg/strategies"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Vates Kube OS (github.com/vatesfr/vates-kube-os-capi-providers),
// experimental: a plain contractFlavor whose control plane machines run
// stacked etcd, so even replica counts are refused.
const VatesControlPlaneKind = "VatesControlPlane"

var VatesControlPlaneGVK = schema.GroupVersionKind{Group: cpGroup, Version: "v1alpha1", Kind: VatesControlPlaneKind}

var vatesKubeOSContract = controlPlaneContract{gvk: VatesControlPlaneGVK}

// vatesKubeOSFlavor is the plain Cluster API control plane contract, except
// for the control plane versions.
type vatesKubeOSFlavor struct {
	contractFlavor
}

// AddInfo rebuilds the control plane versions from its Machines:
// VatesControlPlane reports no status.versions, so CAPI leaves them empty.
func (f vatesKubeOSFlavor) AddInfo(ctx context.Context, _ *capiv1.Cluster, info *strategies.ClusterInfo) error {
	machines := &capiv1.MachineList{}
	if err := f.Cache().List(ctx, machines, client.InNamespace(f.Namespace()),
		client.MatchingFields{uuids.ClusterUUIDIndexKey: info.ID.String()}); err != nil {
		return fmt.Errorf("list Machines: %w", err)
	}
	var controlPlanes []capiv1.Machine
	for _, machine := range machines.Items {
		if _, found := machine.Labels[capiv1.MachineControlPlaneLabel]; found {
			controlPlanes = append(controlPlanes, machine)
		}
	}
	if len(controlPlanes) == 0 {
		return nil
	}
	info.ControlPlanesStatus.Versions = machineVersions(f.Host, controlPlanes)
	// "unknown" is MachineVersion's placeholder, not a version.
	if versions := info.ControlPlanesStatus.Versions; info.Version == "" && len(versions) == 1 && versions[0].Version != "unknown" {
		info.Version = versions[0].Version
	}
	return nil
}
