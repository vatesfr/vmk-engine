package flavor

import (
	"context"
	"fmt"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vatesfr/vmk-engine/pkg/strategies"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// controlPlaneContract describes a control plane provider that follows the
// Cluster API control plane contract: spec.replicas on the referenced object,
// workers as MachineDeployments, the Cluster status filled by CAPI.
type controlPlaneContract struct {
	gvk schema.GroupVersionKind
	// allowsEvenReplicas tells whether the current control plane may run an
	// even number of replicas. Nil means never, as with stacked etcd.
	allowsEvenReplicas func(*unstructured.Unstructured) (bool, error)
}

// contractFlavor is the shared base of the flavors whose provider follows the
// contract; it is embedded, never registered on its own.
type contractFlavor struct {
	Host
	contract controlPlaneContract
}

func (contractFlavor) AddInfo(context.Context, *capiv1.Cluster, *strategies.ClusterInfo) error {
	return nil
}

func (f contractFlavor) SetWorkerReplicas(ctx context.Context, cluster *capiv1.Cluster, replicas int32) error {
	return f.ScaleWorkerDeployment(ctx, cluster, replicas)
}

func (f contractFlavor) Delete(ctx context.Context, cluster *capiv1.Cluster) error {
	return f.DeleteCAPICluster(ctx, cluster)
}

func (f contractFlavor) SetControlPlaneReplicas(ctx context.Context, cluster *capiv1.Cluster, replicas int32) error {
	gvk := f.contract.gvk
	ref := cluster.Spec.ControlPlaneRef
	if ref.Name == "" {
		return scalingConflict(fmt.Sprintf("a %s reference is required", gvk.Kind))
	}
	controlPlane := &unstructured.Unstructured{}
	controlPlane.SetGroupVersionKind(gvk)
	if err := f.Client().Get(ctx, client.ObjectKey{Namespace: f.Namespace(), Name: ref.Name}, controlPlane); err != nil {
		if apierrors.IsNotFound(err) {
			return scalingConflict(fmt.Sprintf("referenced %s is missing", gvk.Kind))
		}
		return fmt.Errorf("get control plane: %w", err)
	}
	if err := f.ValidateScalingObject(controlPlane, cluster); err != nil {
		return err
	}
	if replicas%2 == 0 {
		allowed := false
		if f.contract.allowsEvenReplicas != nil {
			var err error
			if allowed, err = f.contract.allowsEvenReplicas(controlPlane); err != nil {
				return err
			}
		}
		if !allowed {
			return strategies.ErrInvalidControlPlaneReplicas
		}
	}
	current, found, err := unstructured.NestedInt64(controlPlane.Object, specKey, replicasKey)
	if err != nil {
		return fmt.Errorf("read control plane replicas: %w", err)
	}
	if found && current == int64(replicas) {
		return nil
	}
	before := controlPlane.DeepCopy()
	if err := unstructured.SetNestedField(controlPlane.Object, int64(replicas), specKey, replicasKey); err != nil {
		return err
	}
	return f.PatchReplicas(ctx, controlPlane, before)
}

// upgradeControlPlane is refused: the upgrade steps rely on kubeadm resources
// (see kubeadmFlavor).
func (contractFlavor) UpgradeControlPlane(_ context.Context, cluster *capiv1.Cluster) (*unstructured.Unstructured, error) {
	return nil, unsupportedUpgradeConflict(cluster)
}
