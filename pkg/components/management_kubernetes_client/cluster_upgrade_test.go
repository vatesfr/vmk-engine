package management_kubernetes_client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"capv-proxy/pkg/components/cluster_upgrader"
	versionmanager "capv-proxy/pkg/components/version_manager"
	uuids "capv-proxy/pkg/helpers/uuids"
	"capv-proxy/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

const (
	controlPlanePhase    = "control plane"
	workersPhase         = "workers"
	upgradeSource        = "v1.36.4"
	upgradeTargetVersion = "v1.36.5"
	upgradeTestCluster   = "upgrade-demo"
	upgradeFailureCP     = "control plane patch"
)

func upgradeFixtures(name string) (*capiv1.Cluster, *unstructured.Unstructured, *capiv1.MachineDeployment, *unstructured.Unstructured, *capiv1.Machine) {
	labels := map[string]string{capiv1.ClusterNameLabel: name}
	metadata := func(n string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: n, Namespace: testNamespace, UID: types.UID(n), Generation: 1, Labels: labels}
	}
	cluster := &capiv1.Cluster{
		ObjectMeta: metadata(name),
		Spec: capiv1.ClusterSpec{ControlPlaneRef: capiv1.ContractVersionedObjectReference{
			APIGroup: cpGroup,
			Kind:     controlPlaneKind,
			Name:     name + "-cp",
		}},
	}
	config := map[string]any{commandsKey: []any{
		`FULL="` + upgradeSource + `"` + "\n" + `echo "$FULL" > /etc/kubernetes-version`,
		`VERSION=$(sed 's/^v//' /etc/kubernetes-version); dnf install -y "kubelet-$VERSION" "kubeadm-$VERSION"`,
	}}
	cp := resource(cpGroup, controlPlaneKind, name+"-cp", testNamespace, map[string]any{versionKey: upgradeSource, replicasKey: int64(1), kubeadmConfigSpecKey: config})
	cp.SetUID(types.UID(cp.GetName()))
	cp.SetLabels(labels)
	cp.SetGeneration(1)
	cp.Object["status"] = cpStatus(upgradeSource, 1)
	bootstrap := resource(bootstrapGroup, bootstrapTemplateKind, name+"-bootstrap", testNamespace, map[string]any{templateKey: map[string]any{specKey: config}})
	bootstrap.SetUID(types.UID(bootstrap.GetName()))
	bootstrap.SetLabels(labels)
	md := &capiv1.MachineDeployment{
		ObjectMeta: metadata(name + "-workers"),
		Spec: capiv1.MachineDeploymentSpec{
			ClusterName: name,
			Replicas:    ptr.To(int32(1)),
			Template: capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{
				Version: upgradeSource,
				Bootstrap: capiv1.Bootstrap{ConfigRef: capiv1.ContractVersionedObjectReference{
					APIGroup: bootstrapGroup,
					Kind:     bootstrapTemplateKind,
					Name:     bootstrap.GetName(),
				}},
			}},
		},
		Status: mdStatus(upgradeSource, 1),
	}
	machine := &capiv1.Machine{
		ObjectMeta: metadata(name + "-machine"),
		Spec: capiv1.MachineSpec{
			ClusterName: name,
			Version:     upgradeSource,
		},
		Status: capiv1.MachineStatus{
			NodeInfo: &corev1.NodeSystemInfo{KubeletVersion: upgradeSource},
			Conditions: []metav1.Condition{{
				Type:   capiv1.ReadyCondition,
				Status: metav1.ConditionTrue,
				Reason: "Ready",
			}},
		},
	}
	return cluster, cp, md, bootstrap, machine
}

func cpStatus(v string, generation int64) map[string]any {
	return map[string]any{
		"observedGeneration": generation,
		replicasKey:          int64(1),
		"readyReplicas":      int64(1),
		"availableReplicas":  int64(1),
		"upToDateReplicas":   int64(1),
		"versions": []any{map[string]any{
			versionKey:  v,
			replicasKey: int64(1),
		}},
	}
}

func mdStatus(v string, generation int64) capiv1.MachineDeploymentStatus {
	return capiv1.MachineDeploymentStatus{
		ObservedGeneration: generation,
		Replicas:           ptr.To(int32(1)),
		ReadyReplicas:      ptr.To(int32(1)),
		AvailableReplicas:  ptr.To(int32(1)),
		UpToDateReplicas:   ptr.To(int32(1)),
		Versions: []capiv1.StatusVersion{{
			Version:  v,
			Replicas: 1,
		}},
	}
}

func upgradeFake(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objects...).
		WithIndex(&capiv1.Cluster{}, uuids.UUIDIndexKey, clusterUUIDIndexer).
		WithIndex(&capiv1.Machine{}, uuids.UUIDIndexKey, machineUUIDIndexer).
		WithIndex(&capiv1.Machine{}, uuids.ClusterUUIDIndexKey, machineClusterUUIDIndexer).
		WithStatusSubresource(&capiv1.MachineDeployment{}, &capiv1.Machine{}, resource(cpGroup, controlPlaneKind, "", "", nil)).Build()
}

func TestUpgradeRolloutRestartAndIsolation(t *testing.T) {
	cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
	other, otherCP, otherMD, otherBootstrap, otherMachine := upgradeFixtures("untouched")
	k := upgradeFake(t, cluster, cp, md, bootstrap, machine, other, otherCP, otherMD, otherBootstrap, otherMachine)
	c := newUpgradeClusterClient(k, testNamespace)
	ctx := t.Context()
	originalSource := bootstrap.DeepCopy()
	unrelatedBefore := &capiv1.MachineDeployment{}
	require.NoError(t, k.Get(ctx, client.ObjectKeyFromObject(otherMD), unrelatedBefore))
	op, err := startTestUpgrade(ctx, c, cluster.Name, upgradeTargetVersion)
	require.NoError(t, err)
	assert.True(t, op.Locked)
	persisted := &capiv1.Cluster{}
	require.NoError(t, k.Get(ctx, client.ObjectKeyFromObject(cluster), persisted))
	assert.NotContains(t, persisted.Annotations[upgradeAnnotation], `"stage"`)
	same, err := startTestUpgrade(ctx, c, cluster.Name, upgradeTargetVersion)
	require.NoError(t, err)
	assert.Equal(t, op.ID, same.ID)
	_, err = startTestUpgrade(ctx, c, cluster.Name, "v1.37.1")
	require.ErrorIs(t, err, strategies.ErrClusterUpgradeConflict)
	for _, mutate := range []func() error{
		func() error { return c.SetWorkerReplicas(ctx, uuids.ClusterID(cluster.Name), 3) },
		func() error { return c.SetControlPlaneReplicas(ctx, uuids.ClusterID(cluster.Name), 3) },
		func() error { return c.SetPaused(ctx, uuids.ClusterID(cluster.Name), true) },
		func() error { return c.Delete(ctx, uuids.ClusterID(cluster.Name)) },
		func() error {
			return newUpgradeMachineClient(k, testNamespace).DeleteMachine(ctx, uuids.ClusterID(cluster.Name), uuids.NodeID(cluster.Name, machine.Name))
		},
		func() error {
			return newUpgradeMachineClient(k, testNamespace).SetMachinePaused(ctx, uuids.ClusterID(cluster.Name), uuids.NodeID(cluster.Name, machine.Name), true)
		},
	} {
		require.ErrorIs(t, mutate(), strategies.ErrClusterUpgradeConflict)
	}
	require.NoError(t, runUpgradeUntilCheckpoint(ctx, c))
	require.NoError(t, k.Get(ctx, client.ObjectKeyFromObject(cp), cp))
	v, _, _ := unstructured.NestedString(cp.Object, specKey, versionKey)
	assert.Equal(t, upgradeTargetVersion, v)
	commands, _, _ := unstructured.NestedStringSlice(cp.Object, specKey, kubeadmConfigSpecKey, commandsKey)
	assert.Contains(t, commands[0], `FULL="`+upgradeTargetVersion+`"`)
	require.NoError(t, k.Get(ctx, client.ObjectKeyFromObject(md), md))
	assert.Equal(t, upgradeSource, md.Spec.Template.Spec.Version, "workers must wait for observed control plane convergence")
	// A new client represents a restarted API process; all state lives in Kubernetes.
	c = newUpgradeClusterClient(k, testNamespace)
	cp.Object["status"] = cpStatus(upgradeTargetVersion, cp.GetGeneration())
	require.NoError(t, k.Status().Update(ctx, cp))
	require.NoError(t, runUpgradeUntilCheckpoint(ctx, c))
	require.NoError(t, runUpgradeUntilCheckpoint(ctx, c))
	require.NoError(t, k.Get(ctx, client.ObjectKeyFromObject(md), md))
	assert.Equal(t, upgradeTargetVersion, md.Spec.Template.Spec.Version)
	assert.NotEqual(t, bootstrap.GetName(), md.Spec.Template.Spec.Bootstrap.ConfigRef.Name)
	newTemplate := resource(bootstrapGroup, bootstrapTemplateKind, md.Spec.Template.Spec.Bootstrap.ConfigRef.Name, testNamespace, nil)
	require.NoError(t, k.Get(ctx, client.ObjectKeyFromObject(newTemplate), newTemplate))
	commands, _, _ = unstructured.NestedStringSlice(newTemplate.Object, specKey, templateKey, specKey, commandsKey)
	assert.Contains(t, commands[0], upgradeTargetVersion)
	assert.Equal(t, cluster.UID, newTemplate.GetOwnerReferences()[0].UID)
	require.NoError(t, runUpgradeUntilCheckpoint(ctx, c))
	status, err := c.GetUpgrade(ctx, cluster.Name)
	require.NoError(t, err)
	assert.True(t, status.Locked, "old worker status must not release the lock")
	md.Status = mdStatus(upgradeTargetVersion, md.Generation)
	require.NoError(t, k.Status().Update(ctx, md))
	require.NoError(t, runUpgradeUntilCheckpoint(ctx, c))
	status, err = c.GetUpgrade(ctx, cluster.Name)
	require.NoError(t, err)
	assert.True(t, status.Locked, "observed kubelet version must also converge")
	require.NoError(t, k.Get(ctx, client.ObjectKeyFromObject(machine), machine))
	machine.Spec.Version = upgradeTargetVersion
	require.NoError(t, k.Update(ctx, machine))
	machine.Status.NodeInfo.KubeletVersion = upgradeTargetVersion
	require.NoError(t, k.Status().Update(ctx, machine))
	require.NoError(t, runUpgradeUntilCheckpoint(ctx, c))
	status, err = c.GetUpgrade(ctx, cluster.Name)
	require.NoError(t, err)
	assert.False(t, status.Locked)
	assert.NotEmpty(t, status.CompletedAt)
	require.NoError(t, k.Get(ctx, client.ObjectKeyFromObject(cluster), cluster))
	assert.Empty(t, cluster.Labels[upgradeActiveLabel])
	assert.NotContains(t, cluster.Annotations[upgradeAnnotation], `"stage"`)
	assert.False(t, toInfo(cluster).IsUpgrading)
	require.NoError(t, k.Get(ctx, client.ObjectKeyFromObject(bootstrap), bootstrap))
	assert.Equal(t, originalSource.Object[specKey], bootstrap.Object[specKey], "shared source template must remain immutable")
	unrelatedAfter := &capiv1.MachineDeployment{}
	require.NoError(t, k.Get(ctx, client.ObjectKeyFromObject(otherMD), unrelatedAfter))
	assert.Equal(t, unrelatedBefore, unrelatedAfter)
	require.NoError(t, k.Get(ctx, client.ObjectKeyFromObject(otherCP), otherCP))
	otherVersion, _, _ := unstructured.NestedString(otherCP.Object, specKey, versionKey)
	assert.Equal(t, upgradeSource, otherVersion)
	// The lock is released only on success, permitting the next minor upgrade.
	_, err = startTestUpgrade(ctx, c, cluster.Name, "v1.37.1")
	require.NoError(t, err)
}

func TestUpgradeValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(*capiv1.Cluster, *unstructured.Unstructured, *capiv1.MachineDeployment, *unstructured.Unstructured)
		target   string
		expected error
	}{
		{name: "paused", mutate: func(c *capiv1.Cluster, _ *unstructured.Unstructured, _ *capiv1.MachineDeployment, _ *unstructured.Unstructured) {
			c.Spec.Paused = ptr.To(true)
		}},
		{name: "unhealthy", mutate: func(_ *capiv1.Cluster, c *unstructured.Unstructured, _ *capiv1.MachineDeployment, _ *unstructured.Unstructured) {
			delete(c.Object["status"].(map[string]any), "readyReplicas")
		}},
		{name: "mixed versions", mutate: func(_ *capiv1.Cluster, _ *unstructured.Unstructured, m *capiv1.MachineDeployment, _ *unstructured.Unstructured) {
			m.Spec.Template.Spec.Version = "v1.35.9"
			m.Status = mdStatus("v1.35.9", m.Generation)
		}},
		{name: "foreign resource", mutate: func(_ *capiv1.Cluster, c *unstructured.Unstructured, _ *capiv1.MachineDeployment, _ *unstructured.Unstructured) {
			c.SetLabels(map[string]string{capiv1.ClusterNameLabel: "foreign-upgrade-cluster"})
		}},
		{name: "unknown bootstrap", mutate: func(_ *capiv1.Cluster, _ *unstructured.Unstructured, _ *capiv1.MachineDeployment, b *unstructured.Unstructured) {
			require.NoError(t, unstructured.SetNestedStringSlice(b.Object, []string{"dnf install kubelet-1.36.4"}, specKey, templateKey, specKey, commandsKey))
		}},
		{name: "OnDelete", mutate: func(_ *capiv1.Cluster, _ *unstructured.Unstructured, m *capiv1.MachineDeployment, _ *unstructured.Unstructured) {
			m.Spec.Rollout.Strategy.Type = capiv1.OnDeleteMachineDeploymentStrategyType
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
			if tc.mutate != nil {
				tc.mutate(cluster, cp, md, bootstrap)
			}
			c := newUpgradeClusterClient(upgradeFake(t, cluster, cp, md, bootstrap, machine), testNamespace)
			target := tc.target
			if target == "" {
				target = upgradeTargetVersion
			}
			_, err := startTestUpgrade(t.Context(), c, cluster.Name, target)
			expected := tc.expected
			if expected == nil {
				expected = strategies.ErrClusterUpgradeConflict
			}
			require.ErrorIs(t, err, expected)
			_, err = c.GetUpgrade(t.Context(), cluster.Name)
			require.ErrorIs(t, err, strategies.ErrClusterUpgradeNotFound)
		})
	}
}

func TestUpgradeLockAcquisitionRace(t *testing.T) {
	cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
	var pending sync.WaitGroup
	pending.Add(2)
	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, cp, md, bootstrap, machine).
		WithIndex(&capiv1.Cluster{}, uuids.UUIDIndexKey, clusterUUIDIndexer).WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if _, ok := obj.(*capiv1.Cluster); ok {
				pending.Done()
				pending.Wait()
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
	}).Build()
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := startTestUpgrade(t.Context(), newUpgradeClusterClient(k, testNamespace), cluster.Name, upgradeTargetVersion)
			results <- err
		}()
	}
	first, second := <-results, <-results
	if first == nil {
		require.ErrorIs(t, second, strategies.ErrClusterUpgradeConflict)
	} else {
		require.ErrorIs(t, first, strategies.ErrClusterUpgradeConflict)
		require.NoError(t, second)
	}
}

func TestUpgradeRetainsLockAfterFailureAndRetries(t *testing.T) {
	cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
	k := upgradeFake(t, cluster, cp, md, bootstrap, machine)
	c := newUpgradeClusterClient(k, testNamespace)
	_, err := startTestUpgrade(t.Context(), c, cluster.Name, upgradeTargetVersion)
	require.NoError(t, err)
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(cp), cp))
	originalConfig, _, _ := unstructured.NestedMap(cp.Object, specKey, kubeadmConfigSpecKey)
	require.NoError(t, unstructured.SetNestedStringSlice(cp.Object, []string{"unexpected change"}, specKey, kubeadmConfigSpecKey, commandsKey))
	require.NoError(t, k.Update(t.Context(), cp))
	require.Error(t, runUpgradeUntilCheckpoint(t.Context(), c))
	status, err := c.GetUpgrade(t.Context(), cluster.Name)
	require.NoError(t, err)
	assert.True(t, status.Locked)
	assert.Contains(t, status.Message, "changed")
	require.NoError(t, unstructured.SetNestedMap(cp.Object, originalConfig, specKey, kubeadmConfigSpecKey))
	require.NoError(t, k.Update(t.Context(), cp))
	require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), newUpgradeClusterClient(k, testNamespace)))
}

func TestUpgradeBootstrapPreservesUnrelatedText(t *testing.T) {
	input := map[string]any{commandsKey: []any{`FULL="` + upgradeSource + `"` + "\n" + `echo "URL v1.36.4 $(hostname) $HOME"`}}
	result, err := upgradeBootstrap(input, upgradeSource, upgradeTargetVersion)
	require.NoError(t, err)
	raw, _ := json.Marshal(result)
	assert.Contains(t, string(raw), "URL v1.36.4 $(hostname) $HOME")
	original, _ := json.Marshal(input)
	assert.True(t, strings.Contains(string(original), upgradeSource))
	assert.NotContains(t, string(original), upgradeTargetVersion)
}

func TestUpgradeRecoversAmbiguousWrites(t *testing.T) {
	for _, failure := range []string{upgradeFailureCP, "bootstrap creation", "worker patch"} {
		t.Run(failure, func(t *testing.T) {
			cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
			underlying := upgradeFake(t, cluster, cp, md, bootstrap, machine)
			injected := false
			k := interceptor.NewClient(underlying.(client.WithWatch), interceptor.Funcs{
				Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					err := c.Patch(ctx, obj, patch, opts...)
					if err != nil {
						return err
					}
					isCP := obj.GetObjectKind().GroupVersionKind().Kind == controlPlaneKind
					_, isMD := obj.(*capiv1.MachineDeployment)
					if !injected && ((failure == upgradeFailureCP && isCP) || (failure == "worker patch" && isMD)) {
						injected = true
						return errTest
					}
					return nil
				},
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					err := c.Create(ctx, obj, opts...)
					if err == nil && !injected && failure == "bootstrap creation" {
						injected = true
						return errTest
					}
					return err
				},
			})
			c := newUpgradeClusterClient(k, testNamespace)
			_, err := startTestUpgrade(t.Context(), c, cluster.Name, upgradeTargetVersion)
			require.NoError(t, err)
			if failure == upgradeFailureCP {
				require.ErrorIs(t, runUpgradeUntilCheckpoint(t.Context(), c), errTest)
			} else {
				require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), c))
			}
			// An ambiguous control-plane patch is recognized by its target version/hash.
			require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), c))
			require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(cp), cp))
			cp.Object["status"] = cpStatus(upgradeTargetVersion, cp.GetGeneration())
			require.NoError(t, k.Status().Update(t.Context(), cp))
			if failure == upgradeFailureCP {
				require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), c))
			} else {
				require.ErrorIs(t, runUpgradeUntilCheckpoint(t.Context(), c), errTest)
			}
			status, err := c.GetUpgrade(t.Context(), cluster.Name)
			require.NoError(t, err)
			assert.True(t, status.Locked)
			require.True(t, injected)
			// A restarted process handles AlreadyExists or an already patched worker.
			require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), newUpgradeClusterClient(k, testNamespace)))
			require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(md), md))
			assert.Equal(t, upgradeTargetVersion, md.Spec.Template.Spec.Version)
			templates := &unstructured.UnstructuredList{}
			templates.SetAPIVersion(bootstrapGroup + "/v1beta2")
			templates.SetKind(bootstrapTemplateKind + "List")
			require.NoError(t, k.List(t.Context(), templates, client.InNamespace(testNamespace)))
			assert.Len(t, templates.Items, 2, "source plus exactly one deterministic upgrade template")
		})
	}
}

func TestUpgradeSupportsMultipleWorkerDeployments(t *testing.T) {
	cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
	second := md.DeepCopy()
	second.Name += "-second"
	second.UID = types.UID(second.Name)
	k := upgradeFake(t, cluster, cp, md, second, bootstrap, machine)
	c := newUpgradeClusterClient(k, testNamespace)
	_, err := startTestUpgrade(t.Context(), c, cluster.Name, upgradeTargetVersion)
	require.NoError(t, err)
	require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), c))
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(cp), cp))
	cp.Object["status"] = cpStatus(upgradeTargetVersion, cp.GetGeneration())
	require.NoError(t, k.Status().Update(t.Context(), cp))
	require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), c))
	require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), c))
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(md), md))
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(second), second))
	assert.Equal(t, upgradeTargetVersion, md.Spec.Template.Spec.Version)
	assert.Equal(t, upgradeTargetVersion, second.Spec.Template.Spec.Version)
	assert.NotEqual(t, md.Spec.Template.Spec.Bootstrap.ConfigRef.Name, second.Spec.Template.Spec.Bootstrap.ConfigRef.Name)
}

func TestUpgradeRecoversAmbiguousLockAcquisition(t *testing.T) {
	cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
	underlying := upgradeFake(t, cluster, cp, md, bootstrap, machine)
	k := interceptor.NewClient(underlying.(client.WithWatch), interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if err := c.Patch(ctx, obj, patch, opts...); err != nil {
				return err
			}
			return errTest // Simulate a lost response after the API server persisted intent.
		},
	})
	_, err := startTestUpgrade(t.Context(), newUpgradeClusterClient(k, testNamespace), cluster.Name, upgradeTargetVersion)
	require.ErrorIs(t, err, errTest)
	restarted := newUpgradeClusterClient(underlying, testNamespace)
	stored, err := restarted.GetUpgrade(t.Context(), cluster.Name)
	require.NoError(t, err)
	assert.True(t, stored.Locked)
	retried, err := startTestUpgrade(t.Context(), restarted, cluster.Name, upgradeTargetVersion)
	require.NoError(t, err)
	assert.Equal(t, stored.ID, retried.ID)
	require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), restarted))
}

// These tests exercise real persistence and ambiguous writes through the upgrader.
func startTestUpgrade(ctx context.Context, resources ClusterClient, name, target string) (strategies.UpgradeOperation, error) {
	executor := cluster_upgrader.NewClusterUpgrader(ctx, &persistOnlyUpgradeResources{ForManagingClusterResources: resources}, versionmanager.NewVersionManager())
	defer executor.Stop()
	return executor.StartUpgrade(ctx, uuids.ClusterID(name), strings.TrimPrefix(target, "v"))
}

// Initial persistence is exercised through the real upgrader. Hold
// the registered worker at its first read until the test shuts the component down.
type persistOnlyUpgradeResources struct {
	strategies.ForManagingClusterResources
	readInitial sync.Once
}

func (r *persistOnlyUpgradeResources) GetUpgradeContext(ctx context.Context, name string) (strategies.UpgradeContext, error) {
	initial := false
	r.readInitial.Do(func() { initial = true })
	if initial {
		return r.ForManagingClusterResources.GetUpgradeContext(ctx, name)
	}
	<-ctx.Done()
	return strategies.UpgradeContext{}, ctx.Err()
}

func TestUpgradeStorageRejectsStaleSnapshots(t *testing.T) {
	for _, resourceKind := range []string{scalingClusterKind, controlPlaneKind, "MachineDeployment"} {
		t.Run(resourceKind, func(t *testing.T) {
			cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
			k := upgradeFake(t, cluster, cp, md, bootstrap, machine)
			c := newUpgradeClusterClient(k, testNamespace)
			state, err := c.GetUpgradeContext(t.Context(), cluster.Name)
			require.NoError(t, err)
			controlPlane, err := c.GetUpgradeControlPlane(t.Context(), state.Cluster)
			require.NoError(t, err)
			workers, err := c.GetUpgradeWorkers(t.Context(), state.Cluster)
			require.NoError(t, err)
			var changed client.Object
			switch resourceKind {
			case scalingClusterKind:
				changed = cluster
			case controlPlaneKind:
				changed = cp
			default:
				changed = md
			}
			require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(changed), changed))
			changed.SetAnnotations(map[string]string{"test-concurrent-change": "true"})
			require.NoError(t, k.Update(t.Context(), changed))
			if resourceKind == "MachineDeployment" {
				_, err = c.BuildWorkerUpgradeTarget(t.Context(), state.Cluster, workers[0].Resource, upgradeTargetVersion)
			} else {
				_, err = c.BuildControlPlaneUpgradeTarget(t.Context(), state.Cluster, controlPlane.Resource, upgradeTargetVersion)
			}
			require.ErrorIs(t, err, strategies.ErrUpgradeStateChanged)
			if resourceKind == scalingClusterKind {
				err = c.SaveUpgrade(t.Context(), state.Cluster, strategies.UpgradeOperation{})
				require.ErrorIs(t, err, strategies.ErrUpgradeStateChanged)
			}
		})
	}
}

func TestUpgradeContextDoesNotRequireRelatedResources(t *testing.T) {
	cluster, _, _, _, _ := upgradeFixtures(upgradeTestCluster)
	c := newUpgradeClusterClient(upgradeFake(t, cluster), testNamespace)
	context, err := c.GetUpgradeContext(t.Context(), cluster.Name)
	require.NoError(t, err)
	assert.Equal(t, cluster.Name, context.Cluster.Name)
	assert.Equal(t, string(cluster.UID), context.Cluster.UID)
	assert.NotEmpty(t, context.Cluster.ResourceVersion)
	assert.Nil(t, context.Operation)
	// These reads fail independently; reading the operation's persistence context
	// must stay possible even when related CAPI resources have disappeared.
	_, err = c.GetUpgradeControlPlane(t.Context(), context.Cluster)
	require.Error(t, err)
}

func runUpgradeUntilCheckpoint(ctx context.Context, resources ClusterClient) error {
	operation, err := resources.GetUpgrade(ctx, upgradeTestCluster)
	if err != nil {
		return err
	}
	_, err = runUpgradeAttempt(ctx, resources, upgradeTestCluster, operation.ID)
	return err
}

func TestClusterInfoIsUpgrading(t *testing.T) {
	for _, tc := range []struct {
		name, annotation string
		activeLabel      bool
		want             bool
	}{
		{name: "no upgrade"},
		{name: "control plane", annotation: `{"id":"op-1","version":"v1.36.5","locked":true}`, want: true},
		{name: "workers retry", annotation: `{"id":"op-1","version":"v1.36.5","locked":true,"message":"retrying"}`, want: true},
		{name: "completed", annotation: `{"id":"op-1","version":"v1.36.5","locked":false,"completed_at":"2026-10-21T10:00:00Z"}`},
		{name: "invalid operation", annotation: `{`, want: true},
		{name: "missing operation with active lock", activeLabel: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{upgradeAnnotation: tc.annotation}}}
			if tc.activeLabel {
				cluster.Labels = map[string]string{upgradeActiveLabel: upgradeActiveValue}
			}
			assert.Equal(t, tc.want, toInfo(cluster).IsUpgrading)
		})
	}
}

// runUpgradeAttempt cancels after a resource change or persisted diagnostic. The short
// deadline also bounds waits for controller observations.
func runUpgradeAttempt(parent context.Context, resources strategies.ForManagingClusterResources, name, operationID string) (bool, error) {
	state, err := resources.GetUpgradeContext(parent, name)
	if err != nil {
		return false, err
	}
	if state.Operation == nil || !state.Operation.Locked || state.Operation.ID != operationID {
		return true, nil
	}
	ctx, cancel := context.WithTimeout(parent, 100*time.Millisecond)
	defer cancel()
	observed := &checkpointResources{ForManagingClusterResources: resources, cancel: cancel, operationID: operationID}
	executor := cluster_upgrader.NewClusterUpgrader(ctx, observed, versionmanager.NewVersionManager())
	defer executor.Stop()
	_, err = executor.StartUpgrade(parent, uuids.ClusterID(name), strings.TrimPrefix(state.Operation.Version, "v"))
	if err != nil {
		return false, err
	}
	<-ctx.Done()
	executor.Stop()
	return observed.done, observed.err
}

type checkpointResources struct {
	strategies.ForManagingClusterResources
	cancel      context.CancelFunc
	operationID string
	err         error
	done        bool
}

func (r *checkpointResources) GetUpgradeContext(ctx context.Context, name string) (strategies.UpgradeContext, error) {
	state, err := r.ForManagingClusterResources.GetUpgradeContext(ctx, name)
	if err != nil {
		if errors.Is(err, strategies.ErrClusterNotFound) {
			r.done = true
		} else {
			r.err = err
		}
		r.cancel()
	} else if state.Operation == nil || !state.Operation.Locked || state.Operation.ID != r.operationID {
		r.done = true
		r.cancel()
	}
	return state, err
}

func (r *checkpointResources) ApplyControlPlaneUpgrade(ctx context.Context, cluster strategies.ResourceSnapshot, version string, target strategies.UpgradeTarget) error {
	before, err := r.ForManagingClusterResources.GetUpgradeControlPlane(ctx, cluster)
	if err == nil {
		err = r.ForManagingClusterResources.ApplyControlPlaneUpgrade(ctx, cluster, version, target)
	}
	r.recordError(err)
	if err == nil && before.Version != version {
		r.cancel()
	}
	return err
}

func (r *checkpointResources) ApplyWorkerUpgrade(ctx context.Context, cluster strategies.ResourceSnapshot, id, version string, targets []strategies.UpgradeTarget) error {
	before, err := r.GetUpgradeWorkers(ctx, cluster)
	if err == nil {
		err = r.ForManagingClusterResources.ApplyWorkerUpgrade(ctx, cluster, id, version, targets)
	}
	r.recordError(err)
	if err == nil {
		for _, worker := range before {
			if worker.Version != version {
				r.cancel()
				break
			}
		}
	}
	return err
}

func (r *checkpointResources) recordError(err error) {
	if err != nil {
		r.err = err
	}
	if errors.Is(err, strategies.ErrUpgradeStateChanged) {
		r.cancel()
	}
}

func (r *checkpointResources) SaveUpgrade(ctx context.Context, cluster strategies.ResourceSnapshot, operation strategies.UpgradeOperation) error {
	err := r.ForManagingClusterResources.SaveUpgrade(ctx, cluster, operation)
	r.recordError(err)
	if err == nil {
		if r.err == nil && strings.HasPrefix(operation.Message, strategies.ErrClusterUpgradeConflict.Error()) {
			r.err = fmt.Errorf("%w: %s", strategies.ErrClusterUpgradeConflict, operation.Message)
		}
		r.done = !operation.Locked
		r.cancel()
	}
	return err
}

func (r *checkpointResources) GetUpgradeControlPlane(ctx context.Context, cluster strategies.ResourceSnapshot) (strategies.UpgradeResourceState, error) {
	state, err := r.ForManagingClusterResources.GetUpgradeControlPlane(ctx, cluster)
	r.recordError(err)
	return state, err
}

func (r *checkpointResources) IsControlPlaneUpgradeReady(ctx context.Context, cluster strategies.ResourceSnapshot, version string, target strategies.UpgradeTarget) (bool, error) {
	ready, err := r.ForManagingClusterResources.IsControlPlaneUpgradeReady(ctx, cluster, version, target)
	r.recordError(err)
	return ready, err
}

func (r *checkpointResources) IsWorkerUpgradeReady(ctx context.Context, cluster strategies.ResourceSnapshot, id, version string, targets []strategies.UpgradeTarget) (bool, error) {
	ready, err := r.ForManagingClusterResources.IsWorkerUpgradeReady(ctx, cluster, id, version, targets)
	r.recordError(err)
	return ready, err
}

func TestUpgradeObserversNeverWriteAndDetectBootstrapChanges(t *testing.T) {
	for _, role := range []string{controlPlanePhase, workersPhase} {
		t.Run(role, func(t *testing.T) {
			cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
			base := upgradeFake(t, cluster, cp, md, bootstrap, machine)
			allowWrites := true
			k := interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
				Patch: func(ctx context.Context, underlying client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption) error {
					require.True(t, allowWrites, "observing convergence must not patch resources")
					return underlying.Patch(ctx, object, patch, opts...)
				},
				Create: func(ctx context.Context, underlying client.WithWatch, object client.Object, opts ...client.CreateOption) error {
					require.True(t, allowWrites, "observing convergence must not create resources")
					return underlying.Create(ctx, object, opts...)
				},
				Update: func(ctx context.Context, underlying client.WithWatch, object client.Object, opts ...client.UpdateOption) error {
					require.True(t, allowWrites, "observing convergence must not update resources")
					return underlying.Update(ctx, object, opts...)
				},
			})
			resources := newUpgradeClusterClient(k, testNamespace)
			operation, err := startTestUpgrade(t.Context(), resources, cluster.Name, upgradeTargetVersion)
			require.NoError(t, err)
			state, err := resources.GetUpgradeContext(t.Context(), cluster.Name)
			require.NoError(t, err)
			if role == controlPlanePhase {
				require.NoError(t, resources.ApplyControlPlaneUpgrade(t.Context(), state.Cluster, operation.Version, operation.ControlPlane))
			} else {
				require.NoError(t, resources.ApplyWorkerUpgrade(t.Context(), state.Cluster, operation.ID, operation.Version, operation.Workers))
			}
			observe := func() (bool, error) {
				if role == controlPlanePhase {
					return resources.IsControlPlaneUpgradeReady(t.Context(), state.Cluster, operation.Version, operation.ControlPlane)
				}
				return resources.IsWorkerUpgradeReady(t.Context(), state.Cluster, operation.ID, operation.Version, operation.Workers)
			}
			allowWrites = false
			ready, err := observe()
			require.NoError(t, err)
			assert.False(t, ready, "old controller observations must not signal convergence")
			allowWrites = true
			if role == controlPlanePhase {
				require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(cp), cp))
				require.NoError(t, unstructured.SetNestedField(cp.Object, "changed", specKey, kubeadmConfigSpecKey, "unexpected"))
				require.NoError(t, k.Update(t.Context(), cp))
			} else {
				require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(md), md))
				template := resource(bootstrapGroup, bootstrapTemplateKind, md.Spec.Template.Spec.Bootstrap.ConfigRef.Name, testNamespace, nil)
				require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(template), template))
				require.NoError(t, unstructured.SetNestedField(template.Object, "changed", specKey, templateKey, specKey, "unexpected"))
				require.NoError(t, k.Update(t.Context(), template))
			}
			allowWrites = false
			ready, err = observe()
			require.ErrorIs(t, err, strategies.ErrClusterUpgradeConflict)
			assert.False(t, ready)
			latest, err := resources.GetUpgrade(t.Context(), cluster.Name)
			require.NoError(t, err)
			assert.Equal(t, operation, latest, "observation must leave the persisted operation unchanged")
		})
	}
}

func TestReadUpgradeUsesLockAndCompletionWithoutStage(t *testing.T) {
	for _, tc := range []struct {
		name, annotation string
		locked, invalid  bool
	}{
		{name: "active without stage", annotation: `{"id":"op-1","version":"v1.36.5","locked":true}`, locked: true},
		{name: "completed without stage", annotation: `{"id":"op-1","version":"v1.36.5","locked":false,"completed_at":"2026-10-21T10:00:00Z"}`},
		{name: "legacy active", annotation: `{"id":"op-1","version":"v1.36.5","stage":"Workers","locked":true}`, locked: true},
		{name: "legacy completed", annotation: `{"id":"op-1","version":"v1.36.5","stage":"Succeeded","locked":false,"completed_at":"2026-10-21T10:00:00Z"}`},
		{name: "unlocked without completion", annotation: `{"id":"op-1","version":"v1.36.5","locked":false}`, invalid: true},
		{name: "locked with completion", annotation: `{"id":"op-1","version":"v1.36.5","locked":true,"completed_at":"2026-10-21T10:00:00Z"}`, invalid: true},
		{name: "missing identity", annotation: `{"version":"v1.36.5","locked":true}`, invalid: true},
		{name: "missing version", annotation: `{"id":"op-1","locked":true}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{upgradeAnnotation: tc.annotation}}}
			operation, err := readUpgrade(cluster)
			if tc.invalid {
				require.ErrorIs(t, err, strategies.ErrClusterUpgradeConflict)
				assert.True(t, toInfo(cluster).IsUpgrading, "malformed completion state must not bypass the lock")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, operation)
			assert.Equal(t, tc.locked, operation.Locked)
			encoded, err := json.Marshal(operation)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), `"stage"`)
		})
	}
}

func TestMutationsExcludeUpgradeAcrossClients(t *testing.T) {
	const (
		scaleWorkersOperation      = "scale workers"
		scaleControlPlaneOperation = "scale control plane"
		pauseClusterOperation      = "pause cluster"
		deleteClusterOperation     = "delete cluster"
		pauseMachineOperation      = "pause machine"
		deleteMachineOperation     = "delete machine"
	)
	for _, operation := range []string{scaleWorkersOperation, scaleControlPlaneOperation, pauseClusterOperation, deleteClusterOperation, pauseMachineOperation, deleteMachineOperation} {
		t.Run(operation, func(t *testing.T) {
			cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
			other := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "independent", Namespace: testNamespace, UID: "other-uid"}}
			base := upgradeFake(t, cluster, cp, md, bootstrap, machine, other)
			entered, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			signalEntered := sync.OnceFunc(func() { close(entered) })
			defer unblock()
			blockWrite := func(ctx context.Context) error {
				signalEntered()
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			k := interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
				Patch: func(ctx context.Context, underlying client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption) error {
					data, err := patch.Data(object)
					if err != nil {
						return err
					}
					if strings.Contains(string(data), mutationAnnotation) {
						return underlying.Patch(ctx, object, patch, opts...)
					}
					matches := (operation == scaleWorkersOperation && object.GetName() == md.Name) ||
						(operation == scaleControlPlaneOperation && object.GetName() == cp.GetName()) ||
						(operation == pauseClusterOperation && object.GetName() == cluster.Name && strings.Contains(string(data), `"spec"`)) ||
						(operation == pauseMachineOperation && object.GetName() == machine.Name)
					if matches {
						if err := blockWrite(ctx); err != nil {
							return err
						}
					}
					return underlying.Patch(ctx, object, patch, opts...)
				},
				Delete: func(ctx context.Context, underlying client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
					if err := blockWrite(ctx); err != nil {
						return err
					}
					return underlying.Delete(ctx, object, opts...)
				},
			})
			resources := newUpgradeClusterClient(k, testNamespace)
			stateBeforeMutation, err := resources.GetUpgradeContext(t.Context(), cluster.Name)
			require.NoError(t, err)
			finished := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case scaleWorkersOperation:
					err = resources.SetWorkerReplicas(t.Context(), uuids.ClusterID(cluster.Name), 3)
				case scaleControlPlaneOperation:
					err = resources.SetControlPlaneReplicas(t.Context(), uuids.ClusterID(cluster.Name), 3)
				case pauseClusterOperation:
					err = resources.SetPaused(t.Context(), uuids.ClusterID(cluster.Name), true)
				case deleteClusterOperation:
					err = resources.Delete(t.Context(), uuids.ClusterID(cluster.Name))
				case pauseMachineOperation:
					err = newUpgradeMachineClient(k, testNamespace).SetMachinePaused(t.Context(), uuids.ClusterID(cluster.Name), uuids.NodeID(cluster.Name, machine.Name), true)
				case deleteMachineOperation:
					err = newUpgradeMachineClient(k, testNamespace).DeleteMachine(t.Context(), uuids.ClusterID(cluster.Name), uuids.NodeID(cluster.Name, machine.Name))
				}
				finished <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("mutation did not reach its resource write")
			}
			otherClient := newUpgradeClusterClient(k, testNamespace)
			_, err = startTestUpgrade(t.Context(), otherClient, cluster.Name, upgradeTargetVersion)
			require.ErrorIs(t, err, strategies.ErrClusterMutationConflict)
			// An upgrade whose preflight preceded the mutation must also lose its CAS.
			err = otherClient.SaveUpgrade(t.Context(), stateBeforeMutation.Cluster, strategies.UpgradeOperation{
				ID: "stale-operation", Version: upgradeTargetVersion, Locked: true,
			})
			require.ErrorIs(t, err, strategies.ErrUpgradeStateChanged)
			require.ErrorIs(t, otherClient.SetPaused(t.Context(), uuids.ClusterID(cluster.Name), true), strategies.ErrClusterMutationConflict)
			require.NoError(t, otherClient.SetPaused(t.Context(), uuids.ClusterID(other.Name), true), "other clusters remain independent")
			unblock()
			require.NoError(t, <-finished)
			if operation != deleteClusterOperation {
				require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(cluster), cluster))
				assert.NotContains(t, cluster.Annotations, mutationAnnotation)
				assert.NotContains(t, cluster.Annotations, upgradeAnnotation)
			}
		})
	}
}

func TestMutationReservationSurvivesAmbiguousWrite(t *testing.T) {
	cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
	base := upgradeFake(t, cluster, cp, md, bootstrap, machine)
	k := interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
		Patch: func(ctx context.Context, underlying client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption) error {
			err := underlying.Patch(ctx, object, patch, opts...)
			if _, worker := object.(*capiv1.MachineDeployment); worker && err == nil {
				return errTest
			}
			return err
		},
	})
	resources := newUpgradeClusterClient(k, testNamespace)
	err := resources.SetWorkerReplicas(t.Context(), uuids.ClusterID(cluster.Name), 3)
	require.ErrorIs(t, err, errTest)
	require.ErrorIs(t, err, errMutationOutcomeUnknown)
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(md), md))
	assert.EqualValues(t, 3, *md.Spec.Replicas)
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(cluster), cluster))
	assert.NotEmpty(t, cluster.Annotations[mutationAnnotation])
	_, err = startTestUpgrade(t.Context(), newUpgradeClusterClient(k, testNamespace), cluster.Name, upgradeTargetVersion)
	require.ErrorIs(t, err, strategies.ErrClusterMutationConflict)
}

func TestMutationReservationCleanupIgnoresRequestCancellation(t *testing.T) {
	cluster, _, _, _, _ := upgradeFixtures(upgradeTestCluster)
	k := newFakeClient(t, cluster)
	resources := newUpgradeClusterClient(k, testNamespace)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := resources.withClusterMutation(ctx, cluster.Name, func(context.Context) error {
		cancel()
		return errTest // A validation/read error: no ambiguous resource write occurred.
	})
	require.ErrorIs(t, err, errTest)
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(cluster), cluster))
	assert.NotContains(t, cluster.Annotations, mutationAnnotation)
}

func TestLegacyCAPIUpgradeConvergesWithoutAggregatedVersions(t *testing.T) {
	cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
	delete(cp.Object["status"].(map[string]any), "versions")
	cp.Object["status"].(map[string]any)[versionKey] = upgradeSource
	md.Status.Versions = nil
	k := upgradeFake(t, cluster, cp, md, bootstrap, machine)
	resources := newUpgradeClusterClient(k, testNamespace)
	_, err := startTestUpgrade(t.Context(), resources, cluster.Name, upgradeTargetVersion)
	require.NoError(t, err)
	require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), resources))
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(md), md))
	assert.Equal(t, upgradeSource, md.Spec.Template.Spec.Version, "legacy control plane observations must converge first")
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(cp), cp))
	status := cpStatus(upgradeTargetVersion, cp.GetGeneration())
	delete(status, "versions")
	status[versionKey] = upgradeTargetVersion
	cp.Object["status"] = status
	require.NoError(t, k.Status().Update(t.Context(), cp))
	require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), resources))
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(md), md))
	assert.Equal(t, upgradeTargetVersion, md.Spec.Template.Spec.Version)
	// The fake client does not increment CRD generations on spec patches.
	md.Generation++
	require.NoError(t, k.Update(t.Context(), md))
	state, err := resources.GetUpgradeContext(t.Context(), cluster.Name)
	require.NoError(t, err)
	ready, err := resources.IsWorkerUpgradeReady(t.Context(), state.Cluster, state.Operation.ID, upgradeTargetVersion, state.Operation.Workers)
	require.NoError(t, err)
	assert.False(t, ready, "stale generations must not pass the legacy fallback")
	md.Status = mdStatus(upgradeTargetVersion, md.Generation)
	md.Status.Versions = nil
	require.NoError(t, k.Status().Update(t.Context(), md))
	require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), resources))
	operation, err := resources.GetUpgrade(t.Context(), cluster.Name)
	require.NoError(t, err)
	assert.True(t, operation.Locked, "old observed kubelet versions still prevent completion")
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(machine), machine))
	machine.Spec.Version = upgradeTargetVersion
	require.NoError(t, k.Update(t.Context(), machine))
	machine.Status.NodeInfo.KubeletVersion = upgradeTargetVersion
	require.NoError(t, k.Status().Update(t.Context(), machine))
	require.NoError(t, runUpgradeUntilCheckpoint(t.Context(), newUpgradeClusterClient(k, testNamespace)))
	operation, err = resources.GetUpgrade(t.Context(), cluster.Name)
	require.NoError(t, err)
	assert.False(t, operation.Locked)
	assert.NotEmpty(t, operation.CompletedAt)
}

func TestUpgradeWinsAgainstMutationWithEarlierUnlockedSnapshot(t *testing.T) {
	cluster, cp, md, bootstrap, machine := upgradeFixtures(upgradeTestCluster)
	base := upgradeFake(t, cluster, cp, md, bootstrap, machine)
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	k := interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
		Patch: func(ctx context.Context, underlying client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if object.GetAnnotations()[mutationAnnotation] != "" {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return underlying.Patch(ctx, object, patch, opts...)
		},
	})
	finished := make(chan error, 1)
	go func() {
		finished <- newUpgradeClusterClient(k, testNamespace).SetWorkerReplicas(t.Context(), uuids.ClusterID(cluster.Name), 3)
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("mutation did not reach reservation acquisition")
	}
	operation, err := startTestUpgrade(t.Context(), newUpgradeClusterClient(k, testNamespace), cluster.Name, upgradeTargetVersion)
	require.NoError(t, err)
	assert.True(t, operation.Locked)
	unblock()
	require.ErrorIs(t, <-finished, strategies.ErrClusterMutationConflict)
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(md), md))
	assert.EqualValues(t, 1, *md.Spec.Replicas, "a mutation that lost admission must never patch its target")
	require.NoError(t, k.Get(t.Context(), client.ObjectKeyFromObject(cluster), cluster))
	assert.NotContains(t, cluster.Annotations, mutationAnnotation)
	stored, err := readUpgrade(cluster)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.True(t, stored.Locked)
	assert.Equal(t, operation.ID, stored.ID)
}

func newUpgradeClusterClient(k client.Client, namespace string) ClusterClient {
	return NewClusterClient(k, clientAsCache{k}, namespace)
}

func newUpgradeMachineClient(k client.Client, namespace string) MachineClient {
	return NewMachineClient(k, clientAsCache{k}, namespace)
}
