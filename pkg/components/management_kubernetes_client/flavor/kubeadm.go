package flavor

import (
	"context"
	"fmt"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var kubeadmContract = controlPlaneContract{
	gvk:                schema.GroupVersionKind{Group: cpGroup, Version: "v1beta2", Kind: controlPlaneKind},
	allowsEvenReplicas: kubeadmExternalEtcd,
}

// kubeadmFlavor is the Cluster API contract plus upgrades: the management
// client's upgrade steps (cluster_upgrade.go) read and patch kubeadm
// resources, and its cluster creation only builds kubeadm clusters.
type kubeadmFlavor struct {
	contractFlavor
}

func (f kubeadmFlavor) UpgradeControlPlane(ctx context.Context, cluster *capiv1.Cluster) (*unstructured.Unstructured, error) {
	gvk := f.contract.gvk
	ref := cluster.Spec.ControlPlaneRef
	if ref.Name == "" {
		return nil, upgradeConflict(gvk.Kind + " reference required")
	}
	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(gvk)
	if err := f.Client().Get(ctx, client.ObjectKey{Namespace: f.Namespace(), Name: ref.Name}, cp); err != nil {
		return nil, err
	}
	if err := f.ValidateUpgradeMembership(cp, cluster); err != nil {
		return nil, err
	}
	return cp, nil
}

// kubeadmExternalEtcd allows even replicas when etcd runs outside the control plane.
func kubeadmExternalEtcd(controlPlane *unstructured.Unstructured) (bool, error) {
	_, external, err := unstructured.NestedMap(controlPlane.Object, specKey, kubeadmConfigSpecKey, clusterConfigurationKey, "etcd", "external")
	if err != nil {
		return false, fmt.Errorf("read control plane etcd configuration: %w", err)
	}
	return external, nil
}
