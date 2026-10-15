package workload_kubernetes_client

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"capv-proxy/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

func testNodeClient(client kubernetes.Interface) *NodeClient {
	return &NodeClient{clientFor: func(context.Context, string) (kubernetes.Interface, error) { return client, nil }, pollInterval: time.Millisecond}
}

func workloadNode(cordoned bool) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node", UID: "node-uid"}, Spec: corev1.NodeSpec{Unschedulable: cordoned}}
}

func workloadPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pod", Namespace: "default", UID: "pod-uid",
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "rs", Controller: ptr.To(true)}},
		},
		Spec: corev1.PodSpec{NodeName: "node"},
	}
}

func TestCordonIsIdempotentAndPreservesNode(t *testing.T) {
	node := workloadNode(false)
	node.Labels = map[string]string{"keep": "value"}
	client := fake.NewClientset(node)
	service := testNodeClient(client)
	for _, value := range []bool{true, true, false, false} {
		require.NoError(t, service.SetCordon(t.Context(), "cluster", "node", value))
	}
	result, err := client.CoreV1().Nodes().Get(t.Context(), "node", metav1.GetOptions{})
	require.NoError(t, err)
	require.False(t, result.Spec.Unschedulable)
	require.Equal(t, node.Labels, result.Labels)
	patches := 0
	for _, action := range client.Actions() {
		if action.GetVerb() == "patch" {
			patches++
		}
	}
	require.Equal(t, 2, patches)
	require.ErrorIs(t, service.SetCordon(t.Context(), "cluster", "missing", true), strategies.ErrNodeNotFound)
}

func TestCordonFailurePreventsEvictions(t *testing.T) {
	client := fake.NewClientset(workloadNode(false), workloadPod())
	client.PrependReactor("patch", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "node", errors.New("denied"))
	})
	require.ErrorIs(t, testNodeClient(client).Drain(t.Context(), "cluster", "node", strategies.DrainOptions{}), strategies.ErrWorkloadUnavailable)
	for _, action := range client.Actions() {
		require.NotEqual(t, "pods/eviction", action.GetResource().Resource+"/"+action.GetSubresource())
	}
}

func TestDrainEvictsWithUIDAndKeepsCordon(t *testing.T) {
	client := fake.NewClientset(workloadNode(false), workloadPod())
	attempts := 0
	client.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		attempts++
		eviction := action.(ktesting.CreateAction).GetObject().(*policyv1.Eviction)
		require.Equal(t, "pod-uid", string(*eviction.DeleteOptions.Preconditions.UID))
		if attempts == 1 {
			return true, nil, apierrors.NewTooManyRequests("PDB", 0)
		}
		require.NoError(t, client.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), "default", "pod"))
		return true, nil, nil
	})
	require.NoError(t, testNodeClient(client).Drain(t.Context(), "cluster", "node", strategies.DrainOptions{Timeout: time.Second}))
	require.Equal(t, 2, attempts)
	node, err := client.CoreV1().Nodes().Get(t.Context(), "node", metav1.GetOptions{})
	require.NoError(t, err)
	require.True(t, node.Spec.Unschedulable)
}

func TestDrainPDBTimeoutKeepsCordon(t *testing.T) {
	client := fake.NewClientset(workloadNode(false), workloadPod())
	client.PrependReactor("create", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewTooManyRequests("PDB", 0)
	})
	require.ErrorIs(t, testNodeClient(client).Drain(t.Context(), "cluster", "node", strategies.DrainOptions{Timeout: 20 * time.Millisecond}), strategies.ErrDrainTimeout)
	node, err := client.CoreV1().Nodes().Get(t.Context(), "node", metav1.GetOptions{})
	require.NoError(t, err)
	require.True(t, node.Spec.Unschedulable)
}

func TestDrainWaitsForTerminatingPodAndHonorsCancellation(t *testing.T) {
	pod := workloadPod()
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	client := fake.NewClientset(workloadNode(true), pod)
	ctx, cancel := context.WithCancel(t.Context())
	client.PrependReactor("list", "pods", func(ktesting.Action) (bool, runtime.Object, error) { cancel(); return false, nil, nil })
	require.ErrorIs(t, testNodeClient(client).Drain(ctx, "cluster", "node", strategies.DrainOptions{}), context.Canceled)
	for _, action := range client.Actions() {
		require.NotEqual(t, "create", action.GetVerb())
	}
}

func TestDrainRejectsUnsafePodsBeforeAnyEviction(t *testing.T) {
	for _, kind := range []string{"unmanaged", "emptydir", "daemonset"} {
		t.Run(kind, func(t *testing.T) {
			blocked := workloadPod()
			blocked.Name = "blocked"
			blocked.UID = "blocked"
			switch kind {
			case "unmanaged":
				blocked.OwnerReferences = nil
			case "emptydir":
				blocked.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
			case "daemonset":
				blocked.OwnerReferences[0].Kind = daemonSetKind
			}
			client := fake.NewClientset(workloadNode(false), workloadPod(), blocked)
			require.ErrorIs(t, testNodeClient(client).Drain(t.Context(), "cluster", "node", strategies.DrainOptions{}), strategies.ErrDrainBlocked)
			for _, action := range client.Actions() {
				require.NotEqual(t, "create", action.GetVerb())
			}
		})
	}
}

func TestDrainPodOptions(t *testing.T) {
	pod := workloadPod()
	pod.OwnerReferences = nil
	_, err := skipPod(*pod, strategies.DrainOptions{Force: true})
	require.NoError(t, err)
	pod.Spec.Volumes = []corev1.Volume{{VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	_, err = skipPod(*pod, strategies.DrainOptions{Force: true, DeleteEmptyDirData: true})
	require.NoError(t, err)
	pod = workloadPod()
	pod.OwnerReferences[0].Kind = daemonSetKind
	skip, err := skipPod(*pod, strategies.DrainOptions{IgnoreDaemonSets: true})
	require.NoError(t, err)
	require.True(t, skip)
	pod = workloadPod()
	pod.Annotations = map[string]string{corev1.MirrorPodAnnotationKey: "hash"}
	skip, err = skipPod(*pod, strategies.DrainOptions{})
	require.NoError(t, err)
	require.True(t, skip)
}

func TestNodeStatesAndPartialPodFailure(t *testing.T) {
	for _, podFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(podFailure), func(t *testing.T) {
			pod := workloadPod()
			now := metav1.Now()
			pod.DeletionTimestamp = &now
			other := workloadNode(false)
			other.Name = "other"
			client := fake.NewClientset(workloadNode(true), other, pod)
			if podFailure {
				client.PrependReactor("list", "pods", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("unavailable") })
			}
			states, err := testNodeClient(client).GetNodeStates(t.Context(), "cluster")
			require.Equal(t, podFailure, err != nil)
			require.Equal(t, ptr.To(true), states["node"].IsCordon)
			require.Equal(t, ptr.To(false), states["other"].IsCordon)
			require.Equal(t, ptr.To(false), states["other"].IsDraining)
			if podFailure {
				require.Nil(t, states["node"].IsDraining)
			} else {
				require.Equal(t, ptr.To(true), states["node"].IsDraining)
			}
		})
	}
}

type configSource struct {
	value string
	err   error
	calls int
}

func (s *configSource) Get(context.Context, string) (string, error) { s.calls++; return s.value, s.err }

func TestClientReloadsKubeconfigBetweenOperations(t *testing.T) {
	config := func(host string) string {
		return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: cluster
  cluster:
    server: https://%s
contexts:
- name: context
  context:
    cluster: cluster
current-context: context
`, host)
	}
	source := &configSource{value: config("first.example")}
	service := NewNodeClient(source)
	first, err := service.clientFor(t.Context(), "cluster")
	require.NoError(t, err)
	source.value = config("second.example")
	second, err := service.clientFor(t.Context(), "cluster")
	require.NoError(t, err)
	require.Equal(t, "first.example", first.CoreV1().RESTClient().Get().URL().Host)
	require.Equal(t, "second.example", second.CoreV1().RESTClient().Get().URL().Host)
	require.Equal(t, 2, source.calls)
	source.value = "invalid kubeconfig"
	require.ErrorIs(t, service.SetCordon(t.Context(), "cluster", "node", true), strategies.ErrWorkloadUnavailable)
	source.err = errors.New("secret missing")
	require.ErrorIs(t, service.SetCordon(t.Context(), "cluster", "node", true), strategies.ErrWorkloadUnavailable)
}
