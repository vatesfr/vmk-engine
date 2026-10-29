package management_kubernetes_client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vatesfr/vmk-engine/pkg/strategies"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
)

const (
	mutationAnnotation = "vmk.vates.tech/mutation-lock"
	upgradeActiveValue = "true"
	upgradeAnnotation  = "vmk.vates.tech/upgrade"
	upgradeActiveLabel = "vmk.vates.tech/upgrade-active"
)

func upgradeConflict(reason string) error {
	return fmt.Errorf("%w: %s", strategies.ErrClusterUpgradeConflict, reason)
}

func readUpgrade(cluster *capiv1.Cluster) (*strategies.UpgradeOperation, error) {
	raw := cluster.Annotations[upgradeAnnotation]
	if raw == "" {
		if cluster.Labels[upgradeActiveLabel] == upgradeActiveValue {
			return nil, upgradeConflict("upgrade lock is missing its operation")
		}
		return nil, nil
	}
	var op strategies.UpgradeOperation
	if err := json.Unmarshal([]byte(raw), &op); err != nil {
		return nil, upgradeConflict("invalid persisted upgrade; operator intervention required")
	}
	if op.ID == "" || op.Version == "" ||
		(op.Locked && op.CompletedAt != "") || (!op.Locked && op.CompletedAt == "") {
		return nil, upgradeConflict("invalid persisted upgrade state; operator intervention required")
	}
	return &op, nil
}

func ensureNoUpgrade(cluster *capiv1.Cluster) error {
	op, err := readUpgrade(cluster)
	if err != nil {
		return err
	}
	if op != nil && op.Locked {
		return upgradeConflict("cluster is locked by upgrade " + op.ID)
	}
	return nil
}

func (c ClusterClient) GetUpgrade(ctx context.Context, name string) (strategies.UpgradeOperation, error) {
	cluster, err := c.getClusterResource(ctx, name)
	if err != nil {
		return strategies.UpgradeOperation{}, err
	}
	op, err := readUpgrade(cluster)
	if err != nil {
		return strategies.UpgradeOperation{}, err
	}
	if op == nil {
		return strategies.UpgradeOperation{}, strategies.ErrClusterUpgradeNotFound
	}
	return *op, nil
}

// SaveUpgrade checks the Cluster identity and resource version before committing
// the operation and its lock together. Operation lifecycle decisions belong to the upgrader.
func (c ClusterClient) SaveUpgrade(ctx context.Context, snapshot strategies.ResourceSnapshot, operation strategies.UpgradeOperation) error {
	cluster, err := c.getClusterResource(ctx, snapshot.Name)
	if err != nil {
		return err
	}
	if string(cluster.UID) != snapshot.UID || cluster.ResourceVersion != snapshot.ResourceVersion || cluster.Annotations[mutationAnnotation] != "" {
		return strategies.ErrUpgradeStateChanged
	}
	if err := c.saveUpgrade(ctx, cluster, &operation); err != nil {
		if apierrors.IsConflict(err) {
			return fmt.Errorf("%w: %w", strategies.ErrUpgradeStateChanged, err)
		}
		return err
	}
	return nil
}

func (c ClusterClient) saveUpgrade(ctx context.Context, cluster *capiv1.Cluster, op *strategies.UpgradeOperation) error {
	raw, err := json.Marshal(op)
	if err != nil {
		return err
	}
	before := cluster.DeepCopy()
	if cluster.Annotations == nil {
		cluster.Annotations = map[string]string{}
	}
	if cluster.Labels == nil {
		cluster.Labels = map[string]string{}
	}
	cluster.Annotations[upgradeAnnotation] = string(raw)
	if op.Locked {
		cluster.Labels[upgradeActiveLabel] = upgradeActiveValue
	} else {
		delete(cluster.Labels, upgradeActiveLabel)
	}
	return c.kubernetesClient.Patch(ctx, cluster, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

func validateUpgradeMembership(obj client.Object, cluster *capiv1.Cluster) error {
	if obj.GetLabels()[capiv1.ClusterNameLabel] != cluster.Name {
		return upgradeConflict("upgrade resource must carry the cluster membership label")
	}
	for _, owner := range obj.GetOwnerReferences() {
		if owner.Kind != scalingClusterKind {
			continue
		}
		gv, err := schema.ParseGroupVersion(owner.APIVersion)
		if err != nil || gv.Group != capiv1.GroupVersion.Group || owner.Name != cluster.Name || owner.UID != cluster.UID {
			return upgradeConflict("upgrade resource has a different Cluster owner")
		}
	}
	return nil
}

func validateUpgradeObject(obj client.Object, cluster *capiv1.Cluster) error {
	if err := validateScalingObject(obj, cluster); err != nil {
		return upgradeConflict("upgrade resource is paused, deleting or belongs to another cluster")
	}
	return validateUpgradeMembership(obj, cluster)
}

func resourceSnapshot(obj client.Object) strategies.ResourceSnapshot {
	return strategies.ResourceSnapshot{Name: obj.GetName(), UID: string(obj.GetUID()), ResourceVersion: obj.GetResourceVersion()}
}

func resourceUpgradeState(obj client.Object, v string, ready bool) strategies.UpgradeResourceState {
	_, paused := obj.GetAnnotations()[capiv1.PausedAnnotation]
	return strategies.UpgradeResourceState{Resource: resourceSnapshot(obj), Version: v, Ready: ready, Paused: paused, Deleting: !obj.GetDeletionTimestamp().IsZero()}
}

func (c ClusterClient) GetUpgradeContext(ctx context.Context, name string) (strategies.UpgradeContext, error) {
	result := strategies.UpgradeContext{}
	cluster, err := c.getClusterResource(ctx, name)
	if err != nil {
		return result, err
	}
	if cluster.Annotations[mutationAnnotation] != "" {
		return result, strategies.ErrClusterMutationConflict
	}
	result.Cluster = resourceSnapshot(cluster)
	result.Operation, err = readUpgrade(cluster)
	if err != nil {
		return result, err
	}
	_, paused := cluster.Annotations[capiv1.PausedAnnotation]
	result.Paused = paused || ptr.Deref(cluster.Spec.Paused, false)
	result.Deleting = !cluster.DeletionTimestamp.IsZero()
	result.TopologyManaged = cluster.Spec.Topology.IsDefined()
	return result, nil
}

func (c ClusterClient) GetUpgradeControlPlane(ctx context.Context, snapshot strategies.ResourceSnapshot) (strategies.UpgradeResourceState, error) {
	cluster, err := c.upgradeClusterSnapshot(ctx, snapshot)
	if err != nil {
		return strategies.UpgradeResourceState{}, err
	}
	cp, err := c.upgradeControlPlaneResource(ctx, cluster)
	if err != nil {
		return strategies.UpgradeResourceState{}, err
	}
	current, _, _ := unstructured.NestedString(cp.Object, specKey, versionKey)
	return resourceUpgradeState(cp, current, upgradeResourceReady(cp)), nil
}

func (c ClusterClient) GetUpgradeWorkers(ctx context.Context, snapshot strategies.ResourceSnapshot) ([]strategies.UpgradeWorkerState, error) {
	cluster, err := c.upgradeClusterSnapshot(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	deployments := &capiv1.MachineDeploymentList{}
	if err := c.kubernetesClient.List(ctx, deployments, client.InNamespace(c.namespace), client.MatchingLabels{capiv1.ClusterNameLabel: cluster.Name}); err != nil {
		return nil, err
	}
	sort.Slice(deployments.Items, func(i, j int) bool { return deployments.Items[i].Name < deployments.Items[j].Name })
	workers := make([]strategies.UpgradeWorkerState, 0, len(deployments.Items))
	for i := range deployments.Items {
		md := &deployments.Items[i]
		if err := validateUpgradeMembership(md, cluster); err != nil {
			return nil, err
		}
		if md.Spec.ClusterName != cluster.Name {
			return nil, upgradeConflict("worker deployment has inconsistent membership")
		}
		worker := strategies.UpgradeWorkerState{UpgradeResourceState: resourceUpgradeState(md, md.Spec.Template.Spec.Version, workerReady(md)), RolloutStrategy: string(md.Spec.Rollout.Strategy.Type)}
		worker.Paused = worker.Paused || ptr.Deref(md.Spec.Paused, false)
		workers = append(workers, worker)
	}
	return workers, nil
}

func (c ClusterClient) GetUpgradeMachines(ctx context.Context, snapshot strategies.ResourceSnapshot) ([]strategies.UpgradeMachineState, error) {
	cluster, err := c.upgradeClusterSnapshot(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	machines := &capiv1.MachineList{}
	if err := c.kubernetesClient.List(ctx, machines, client.InNamespace(c.namespace), client.MatchingLabels{capiv1.ClusterNameLabel: cluster.Name}); err != nil {
		return nil, err
	}
	nodes := make([]strategies.UpgradeMachineState, 0, len(machines.Items))
	for i := range machines.Items {
		machine := &machines.Items[i]
		if err := validateUpgradeMembership(machine, cluster); err != nil {
			return nil, err
		}
		if machine.Spec.ClusterName != cluster.Name {
			return nil, upgradeConflict("Machine has inconsistent membership")
		}
		_, paused := machine.Annotations[capiv1.PausedAnnotation]
		node := strategies.UpgradeMachineState{
			Version: machine.Spec.Version,
			Ready:   apimeta.IsStatusConditionTrue(machine.Status.Conditions, capiv1.ReadyCondition),
			Paused:  paused, Deleting: !machine.DeletionTimestamp.IsZero(),
		}
		if machine.Status.NodeInfo != nil {
			node.KubeletVersion = machine.Status.NodeInfo.KubeletVersion
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func (c ClusterClient) ListActiveUpgrades(ctx context.Context) ([]string, error) {
	clusters := &capiv1.ClusterList{}
	if err := c.kubernetesClient.List(ctx, clusters, client.InNamespace(c.namespace), client.MatchingLabels{upgradeActiveLabel: upgradeActiveValue}); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(clusters.Items))
	for i := range clusters.Items {
		names = append(names, clusters.Items[i].Name)
	}
	return names, nil
}

func (c ClusterClient) upgradeControlPlaneResource(ctx context.Context, cluster *capiv1.Cluster) (*unstructured.Unstructured, error) {
	ref := cluster.Spec.ControlPlaneRef
	if ref.APIGroup != cpGroup || ref.Kind != controlPlaneKind || ref.Name == "" {
		return nil, upgradeConflict("KubeadmControlPlane reference required")
	}
	cp := resource(cpGroup, controlPlaneKind, ref.Name, c.namespace, nil)
	if err := c.kubernetesClient.Get(ctx, client.ObjectKeyFromObject(cp), cp); err != nil {
		return nil, err
	}
	if err := validateUpgradeMembership(cp, cluster); err != nil {
		return nil, err
	}
	return cp, nil
}

func configHash(config map[string]any) string {
	raw, _ := json.Marshal(config) // Kubernetes JSON maps contain only JSON values.
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Support the exact version anchor generated by this service, rather than
// replacing arbitrary version-like strings in user commands or unrelated URLs.
// Refuse unknown bootstraps before acquiring a lock or triggering a rollout.
func upgradeBootstrap(config map[string]any, from, target string) (map[string]any, error) {
	result := (&unstructured.Unstructured{Object: config}).DeepCopy().Object
	commands, found, err := unstructured.NestedStringSlice(result, commandsKey)
	if err != nil || !found || len(commands) == 0 {
		return nil, upgradeConflict("bootstrap must install Kubernetes through the managed FULL version anchor")
	}
	old := `FULL="` + from + `"`
	replacement := `FULL="` + target + `"`
	anchors := 0
	for i, command := range commands {
		for _, line := range strings.Split(command, "\n") {
			if strings.TrimSpace(line) == old {
				anchors++
			}
		}
		lines := strings.Split(command, "\n")
		for j, line := range lines {
			if strings.TrimSpace(line) == old {
				lines[j] = strings.Replace(line, old, replacement, 1)
			}
		}
		commands[i] = strings.Join(lines, "\n")
	}
	if anchors != 1 {
		return nil, upgradeConflict("bootstrap requires exactly one managed FULL version anchor matching its current version")
	}
	if err := unstructured.SetNestedStringSlice(result, commands, commandsKey); err != nil {
		return nil, err
	}
	return result, nil
}

func (c ClusterClient) BuildControlPlaneUpgradeTarget(ctx context.Context, snapshot, expected strategies.ResourceSnapshot, target string) (strategies.UpgradeTarget, error) {
	cluster, err := c.upgradeClusterSnapshot(ctx, snapshot)
	if err != nil {
		return strategies.UpgradeTarget{}, err
	}
	cp, err := c.upgradeControlPlaneResource(ctx, cluster)
	if err != nil {
		return strategies.UpgradeTarget{}, err
	}
	if resourceSnapshot(cp) != expected {
		return strategies.UpgradeTarget{}, strategies.ErrUpgradeStateChanged
	}
	config, _, err := unstructured.NestedMap(cp.Object, specKey, kubeadmConfigSpecKey)
	if err != nil {
		return strategies.UpgradeTarget{}, err
	}
	current, _, _ := unstructured.NestedString(cp.Object, specKey, versionKey)
	rendered, err := upgradeBootstrap(config, current, target)
	if err != nil {
		return strategies.UpgradeTarget{}, err
	}
	return strategies.UpgradeTarget{Name: cp.GetName(), UID: string(cp.GetUID()), Version: current, SourceHash: configHash(config), TargetHash: configHash(rendered)}, nil
}

func (c ClusterClient) BuildWorkerUpgradeTarget(ctx context.Context, snapshot, expected strategies.ResourceSnapshot, target string) (strategies.UpgradeTarget, error) {
	cluster, err := c.upgradeClusterSnapshot(ctx, snapshot)
	if err != nil {
		return strategies.UpgradeTarget{}, err
	}
	md := &capiv1.MachineDeployment{}
	if err := c.kubernetesClient.Get(ctx, client.ObjectKey{Namespace: c.namespace, Name: expected.Name}, md); err != nil {
		return strategies.UpgradeTarget{}, err
	}
	if resourceSnapshot(md) != expected {
		return strategies.UpgradeTarget{}, strategies.ErrUpgradeStateChanged
	}
	if md.Spec.ClusterName != cluster.Name {
		return strategies.UpgradeTarget{}, upgradeConflict("worker deployment has inconsistent membership")
	}
	if err := validateUpgradeObject(md, cluster); err != nil {
		return strategies.UpgradeTarget{}, err
	}
	source, err := c.workerBootstrap(ctx, cluster, md)
	if err != nil {
		return strategies.UpgradeTarget{}, err
	}
	config, _, err := unstructured.NestedMap(source.Object, specKey, templateKey, specKey)
	if err != nil {
		return strategies.UpgradeTarget{}, err
	}
	current := md.Spec.Template.Spec.Version
	rendered, err := upgradeBootstrap(config, current, target)
	if err != nil {
		return strategies.UpgradeTarget{}, err
	}
	return strategies.UpgradeTarget{
		Name: md.Name, UID: string(md.UID), Version: current,
		BootstrapName: source.GetName(), BootstrapUID: string(source.GetUID()),
		SourceHash: configHash(config), TargetHash: configHash(rendered),
	}, nil
}

func (c ClusterClient) upgradeClusterSnapshot(ctx context.Context, snapshot strategies.ResourceSnapshot) (*capiv1.Cluster, error) {
	cluster, err := c.getClusterResource(ctx, snapshot.Name)
	if err != nil {
		return nil, err
	}
	if string(cluster.UID) != snapshot.UID || cluster.ResourceVersion != snapshot.ResourceVersion {
		return nil, strategies.ErrUpgradeStateChanged
	}
	return cluster, nil
}

func (c ClusterClient) workerBootstrap(ctx context.Context, cluster *capiv1.Cluster, md *capiv1.MachineDeployment) (*unstructured.Unstructured, error) {
	ref := md.Spec.Template.Spec.Bootstrap.ConfigRef
	if ref.APIGroup != bootstrapGroup || ref.Kind != bootstrapTemplateKind || ref.Name == "" {
		return nil, upgradeConflict("worker requires a KubeadmConfigTemplate")
	}
	source := resource(bootstrapGroup, bootstrapTemplateKind, ref.Name, c.namespace, nil)
	if err := c.kubernetesClient.Get(ctx, client.ObjectKeyFromObject(source), source); err != nil {
		return nil, err
	}
	if err := validateUpgradeObject(source, cluster); err != nil {
		return nil, err
	}
	return source, nil
}

func workerReady(md *capiv1.MachineDeployment) bool {
	desired := ptr.Deref(md.Spec.Replicas, 0)
	return desired > 0 && md.Status.ObservedGeneration >= md.Generation &&
		ptr.Deref(md.Status.Replicas, 0) == desired && ptr.Deref(md.Status.ReadyReplicas, 0) == desired &&
		ptr.Deref(md.Status.AvailableReplicas, 0) == desired && ptr.Deref(md.Status.UpToDateReplicas, 0) == desired &&
		(len(md.Status.Versions) == 0 ||
			(len(md.Status.Versions) == 1 && md.Status.Versions[0].Version == md.Spec.Template.Spec.Version && md.Status.Versions[0].Replicas == desired))
}

func upgradeResourceReady(cp *unstructured.Unstructured) bool {
	desired, _, _ := unstructured.NestedInt64(cp.Object, specKey, replicasKey)
	observed, found, _ := unstructured.NestedInt64(cp.Object, "status", "observedGeneration")
	if desired < 1 || !found || observed < cp.GetGeneration() {
		return false
	}
	for _, field := range []string{replicasKey, "readyReplicas", "availableReplicas", "upToDateReplicas"} {
		n, found, _ := unstructured.NestedInt64(cp.Object, "status", field)
		if !found || n != desired {
			return false
		}
	}
	versions, _, _ := unstructured.NestedSlice(cp.Object, "status", "versions")
	expected, _, _ := unstructured.NestedString(cp.Object, specKey, versionKey)
	if len(versions) == 0 {
		// CAPI 1.13 reports the minimum control-plane version through status.version.
		observedVersion, _, _ := unstructured.NestedString(cp.Object, "status", versionKey)
		return observedVersion == expected
	}
	if len(versions) != 1 {
		return false
	}
	item, ok := versions[0].(map[string]any)
	if !ok {
		return false
	}
	return item[versionKey] == expected && item[replicasKey] == desired
}

// ApplyControlPlaneUpgrade applies the target version and bootstrap idempotently.
func (c ClusterClient) ApplyControlPlaneUpgrade(ctx context.Context, snapshot strategies.ResourceSnapshot, version string, target strategies.UpgradeTarget) error {
	cluster, err := c.upgradeClusterSnapshot(ctx, snapshot)
	if err != nil {
		return err
	}
	cp, err := c.upgradeControlPlaneResource(ctx, cluster)
	if err != nil {
		return err
	}
	if cp.GetName() != target.Name || string(cp.GetUID()) != target.UID {
		return upgradeConflict("control plane identity changed")
	}
	if err := validateUpgradeObject(cp, cluster); err != nil {
		return err
	}
	if err := c.patchUpgradeControlPlane(ctx, cp, version, target); err != nil {
		return upgradeWriteError(err)
	}
	return nil
}

func (c ClusterClient) ApplyWorkerUpgrade(ctx context.Context, snapshot strategies.ResourceSnapshot, operationID, version string, targets []strategies.UpgradeTarget) error {
	cluster, err := c.upgradeClusterSnapshot(ctx, snapshot)
	if err != nil {
		return err
	}
	deployments := &capiv1.MachineDeploymentList{}
	if err := c.kubernetesClient.List(ctx, deployments, client.InNamespace(c.namespace), client.MatchingLabels{capiv1.ClusterNameLabel: cluster.Name}); err != nil {
		return err
	}
	if len(deployments.Items) != len(targets) {
		return upgradeConflict("worker deployment membership changed during upgrade")
	}
	for _, planned := range targets {
		md := &capiv1.MachineDeployment{}
		if err := c.kubernetesClient.Get(ctx, client.ObjectKey{Namespace: c.namespace, Name: planned.Name}, md); err != nil {
			return err
		}
		if string(md.UID) != planned.UID || md.Spec.ClusterName != cluster.Name || ptr.Deref(md.Spec.Paused, false) {
			return upgradeConflict("worker deployment identity or pause state changed")
		}
		if err := validateUpgradeObject(md, cluster); err != nil {
			return err
		}
		if err := c.patchUpgradeWorker(ctx, cluster, md, planned, operationID, version); err != nil {
			return upgradeWriteError(err)
		}
	}
	return nil
}

// IsControlPlaneUpgradeReady observes the target identity, bootstrap and rollout
// without patching resources, even when the requested version is not applied.
func (c ClusterClient) IsControlPlaneUpgradeReady(ctx context.Context, snapshot strategies.ResourceSnapshot, version string, target strategies.UpgradeTarget) (bool, error) {
	cluster, err := c.upgradeClusterSnapshot(ctx, snapshot)
	if err != nil {
		return false, err
	}
	cp, err := c.upgradeControlPlaneResource(ctx, cluster)
	if err != nil {
		return false, err
	}
	if cp.GetName() != target.Name || string(cp.GetUID()) != target.UID {
		return false, upgradeConflict("control plane identity changed")
	}
	if err := validateUpgradeObject(cp, cluster); err != nil {
		return false, err
	}
	current, _, _ := unstructured.NestedString(cp.Object, specKey, versionKey)
	config, _, err := unstructured.NestedMap(cp.Object, specKey, kubeadmConfigSpecKey)
	if err != nil {
		return false, err
	}
	if current != version || configHash(config) != target.TargetHash {
		return false, upgradeConflict("target control plane version or bootstrap changed")
	}
	return upgradeResourceReady(cp), nil
}

// IsWorkerUpgradeReady validates target deployments and their observed rollout
// without creating templates or patching deployments.
func (c ClusterClient) IsWorkerUpgradeReady(ctx context.Context, snapshot strategies.ResourceSnapshot, operationID, version string, targets []strategies.UpgradeTarget) (bool, error) {
	cluster, err := c.upgradeClusterSnapshot(ctx, snapshot)
	if err != nil {
		return false, err
	}
	deployments := &capiv1.MachineDeploymentList{}
	if err := c.kubernetesClient.List(ctx, deployments, client.InNamespace(c.namespace), client.MatchingLabels{capiv1.ClusterNameLabel: cluster.Name}); err != nil {
		return false, err
	}
	if len(deployments.Items) != len(targets) {
		return false, upgradeConflict("worker deployment membership changed during upgrade")
	}
	ready := true
	for _, planned := range targets {
		md := &capiv1.MachineDeployment{}
		if err := c.kubernetesClient.Get(ctx, client.ObjectKey{Namespace: c.namespace, Name: planned.Name}, md); err != nil {
			return false, err
		}
		if string(md.UID) != planned.UID || md.Spec.ClusterName != cluster.Name || ptr.Deref(md.Spec.Paused, false) {
			return false, upgradeConflict("worker deployment identity or pause state changed")
		}
		if err := validateUpgradeObject(md, cluster); err != nil {
			return false, err
		}
		if md.Spec.Template.Spec.Version != version || md.Spec.Template.Spec.Bootstrap.ConfigRef.Name != upgradeTemplateName(cluster, planned, operationID) {
			return false, upgradeConflict("target worker version or bootstrap reference changed")
		}
		source, err := c.workerBootstrap(ctx, cluster, md)
		if err != nil {
			return false, err
		}
		config, _, err := unstructured.NestedMap(source.Object, specKey, templateKey, specKey)
		if err != nil {
			return false, err
		}
		if configHash(config) != planned.TargetHash {
			return false, upgradeConflict("target worker bootstrap changed")
		}
		ready = ready && workerReady(md)
	}
	return ready, nil
}

func upgradeWriteError(err error) error {
	if apierrors.IsConflict(err) {
		return fmt.Errorf("%w: %w", strategies.ErrUpgradeStateChanged, err)
	}
	return err
}

func (c ClusterClient) patchUpgradeControlPlane(ctx context.Context, cp *unstructured.Unstructured, target string, planned strategies.UpgradeTarget) error {
	current, _, _ := unstructured.NestedString(cp.Object, specKey, versionKey)
	config, _, err := unstructured.NestedMap(cp.Object, specKey, kubeadmConfigSpecKey)
	if err != nil {
		return err
	}
	if current == target {
		if configHash(config) != planned.TargetHash {
			return upgradeConflict("target control plane bootstrap changed")
		}
		return nil
	}
	if current != planned.Version || configHash(config) != planned.SourceHash {
		return upgradeConflict("source control plane version or bootstrap changed")
	}
	rendered, err := upgradeBootstrap(config, current, target)
	if err != nil {
		return err
	}
	before := cp.DeepCopy()
	if err := unstructured.SetNestedMap(cp.Object, rendered, specKey, kubeadmConfigSpecKey); err != nil {
		return err
	}
	if err := unstructured.SetNestedField(cp.Object, target, specKey, versionKey); err != nil {
		return err
	}
	return c.kubernetesClient.Patch(ctx, cp, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

func upgradeTemplateName(cluster *capiv1.Cluster, planned strategies.UpgradeTarget, operationID string) string {
	sum := sha256.Sum256([]byte(string(cluster.UID) + "/" + planned.UID + "/" + operationID))
	return "upgrade-" + hex.EncodeToString(sum[:16])
}

func (c ClusterClient) patchUpgradeWorker(ctx context.Context, cluster *capiv1.Cluster, md *capiv1.MachineDeployment, planned strategies.UpgradeTarget, operationID, target string) error {
	targetName := upgradeTemplateName(cluster, planned, operationID)
	ref := md.Spec.Template.Spec.Bootstrap.ConfigRef
	if md.Spec.Template.Spec.Version == target && ref.Name == targetName {
		source, err := c.workerBootstrap(ctx, cluster, md)
		if err != nil {
			return err
		}
		config, _, err := unstructured.NestedMap(source.Object, specKey, templateKey, specKey)
		if err != nil {
			return err
		}
		if configHash(config) != planned.TargetHash {
			return upgradeConflict("target worker bootstrap changed")
		}
		return nil
	}
	if md.Spec.Template.Spec.Version != planned.Version || ref.Name != planned.BootstrapName {
		return upgradeConflict("worker version or bootstrap reference changed")
	}
	source, err := c.workerBootstrap(ctx, cluster, md)
	if err != nil {
		return err
	}
	if string(source.GetUID()) != planned.BootstrapUID {
		return upgradeConflict("source bootstrap was replaced")
	}
	config, _, err := unstructured.NestedMap(source.Object, specKey, templateKey, specKey)
	if err != nil {
		return err
	}
	if configHash(config) != planned.SourceHash {
		return upgradeConflict("source worker bootstrap changed")
	}
	rendered, err := upgradeBootstrap(config, planned.Version, target)
	if err != nil {
		return err
	}
	template := source.DeepCopy()
	template.SetName(targetName)
	template.SetUID("")
	template.SetResourceVersion("")
	template.SetGeneration(0)
	template.SetCreationTimestamp(metav1.Time{})
	template.SetManagedFields(nil)
	template.SetFinalizers(nil)
	template.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: capiv1.GroupVersion.String(), Kind: scalingClusterKind, Name: cluster.Name, UID: cluster.UID}})
	delete(template.Object, "status")
	if err := unstructured.SetNestedMap(template.Object, rendered, specKey, templateKey, specKey); err != nil {
		return err
	}
	if err := c.kubernetesClient.Create(ctx, template); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		existing := resource(bootstrapGroup, bootstrapTemplateKind, targetName, c.namespace, nil)
		if err := c.kubernetesClient.Get(ctx, client.ObjectKeyFromObject(existing), existing); err != nil {
			return err
		}
		if err := validateUpgradeObject(existing, cluster); err != nil {
			return err
		}
		if !reflect.DeepEqual(existing.Object[specKey], template.Object[specKey]) || !reflect.DeepEqual(existing.GetOwnerReferences(), template.GetOwnerReferences()) {
			return upgradeConflict("upgrade template name is occupied by a different resource")
		}
	}
	before := md.DeepCopy()
	md.Spec.Template.Spec.Version = target
	md.Spec.Template.Spec.Bootstrap.ConfigRef.Name = targetName
	return c.kubernetesClient.Patch(ctx, md, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

// withClusterMutation reserves the Cluster before changing another resource.
// Upgrade acquisition uses the same Cluster resourceVersion and refuses this lock.
func (c ClusterClient) withClusterMutation(ctx context.Context, name string, action func(context.Context) error) error {
	cluster, err := c.getClusterResource(ctx, name)
	if err != nil {
		return err
	}
	if err := ensureNoUpgrade(cluster); err != nil {
		return err
	}
	if cluster.Annotations[mutationAnnotation] != "" {
		return strategies.ErrClusterMutationConflict
	}
	token, err := uuid.NewV4()
	if err != nil {
		return err
	}
	before := cluster.DeepCopy()
	if cluster.Annotations == nil {
		cluster.Annotations = make(map[string]string)
	}
	cluster.Annotations[mutationAnnotation] = token.String()
	if err := c.kubernetesClient.Patch(ctx, cluster, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		cleanupErr := c.releaseClusterMutation(ctx, cluster, token.String())
		if apierrors.IsConflict(err) {
			err = fmt.Errorf("%w: %w", strategies.ErrClusterMutationConflict, err)
		}
		return errors.Join(err, cleanupErr)
	}
	err = action(ctx)
	// A timed-out write may still commit. Keep its reservation until an operator
	// verifies its outcome; expiring it could allow a late write during an upgrade.
	if errors.Is(err, errMutationOutcomeUnknown) {
		return err
	}
	return errors.Join(err, c.releaseClusterMutation(ctx, cluster, token.String()))
}

func (c ClusterClient) releaseClusterMutation(parent context.Context, locked *capiv1.Cluster, token string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		current := &capiv1.Cluster{}
		if err := c.kubernetesClient.Get(ctx, client.ObjectKeyFromObject(locked), current); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if current.UID != locked.UID || current.Annotations[mutationAnnotation] != token {
			return nil
		}
		before := current.DeepCopy()
		delete(current.Annotations, mutationAnnotation)
		return c.kubernetesClient.Patch(ctx, current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
}

var errMutationOutcomeUnknown = errors.New("mutation outcome is unknown; cluster reservation retained")

func mutationWriteError(err error) error {
	if err == nil {
		return nil
	}
	if apierrors.IsConflict(err) || apierrors.IsNotFound(err) || apierrors.IsForbidden(err) ||
		apierrors.IsUnauthorized(err) || apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) ||
		apierrors.IsAlreadyExists(err) || apierrors.IsTooManyRequests(err) || apierrors.IsMethodNotSupported(err) {
		return err
	}
	return errors.Join(errMutationOutcomeUnknown, err)
}
