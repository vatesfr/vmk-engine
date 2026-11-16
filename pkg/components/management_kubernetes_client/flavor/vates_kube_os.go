package flavor

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Vates Kube OS (github.com/vatesfr/vates-kube-os-capi-providers),
// experimental: a plain contractFlavor whose control plane machines run
// stacked etcd, so even replica counts are refused.
const VatesControlPlaneKind = "VatesControlPlane"

var VatesControlPlaneGVK = schema.GroupVersionKind{Group: cpGroup, Version: "v1alpha1", Kind: VatesControlPlaneKind}

var vatesKubeOSContract = controlPlaneContract{gvk: VatesControlPlaneGVK}

// vatesKubeOSFlavor is the plain Cluster API control plane contract; it adds
// nothing to contractFlavor for now.
type vatesKubeOSFlavor struct {
	contractFlavor
}
