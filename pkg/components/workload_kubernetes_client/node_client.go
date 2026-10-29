package workload_kubernetes_client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vatesfr/vmk-engine/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
)

const daemonSetKind = "DaemonSet"

// NodeClient creates a separate connection to the workload cluster for each operation.
type NodeClient struct {
	clientFor    func(context.Context, string) (kubernetes.Interface, error)
	pollInterval time.Duration
}

var (
	_ strategies.ForManagingWorkloadNodes     = (*NodeClient)(nil)
	_ strategies.ForGettingWorkloadNodeStates = (*NodeClient)(nil)
)

func NewNodeClient(configs strategies.ForGettingClusterConfig) *NodeClient {
	return &NodeClient{
		pollInterval: time.Second,
		clientFor: func(ctx context.Context, clusterName string) (kubernetes.Interface, error) {
			kubeconfig, err := configs.Get(ctx, clusterName)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", strategies.ErrWorkloadUnavailable, err)
			}
			config, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
			if err != nil {
				return nil, fmt.Errorf("%w: invalid kubeconfig", strategies.ErrWorkloadUnavailable)
			}
			config.Timeout = 30 * time.Second
			client, err := kubernetes.NewForConfig(config)
			if err != nil {
				return nil, fmt.Errorf("%w: cannot create client", strategies.ErrWorkloadUnavailable)
			}
			return client, nil
		},
	}
}

func (c *NodeClient) SetCordon(ctx context.Context, clusterName, nodeName string, cordoned bool) error {
	client, err := c.clientFor(ctx, clusterName)
	if err != nil {
		return err
	}
	return setCordon(ctx, client, nodeName, cordoned)
}

func setCordon(ctx context.Context, client kubernetes.Interface, name string, cordoned bool) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		node, err := client.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if node.Spec.Unschedulable == cordoned {
			return nil
		}
		patch := []byte(fmt.Sprintf(`{"metadata":{"uid":%q,"resourceVersion":%q},"spec":{"unschedulable":%t}}`, node.UID, node.ResourceVersion, cordoned))
		_, err = client.CoreV1().Nodes().Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
		return err
	})
	return operationError(err)
}

// GetNodeStates lists each resource once. Failed pod reads preserve known cordon states.
func (c *NodeClient) GetNodeStates(ctx context.Context, clusterName string) (map[string]strategies.WorkloadNodeState, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client, err := c.clientFor(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, operationError(err)
	}
	result := make(map[string]strategies.WorkloadNodeState, len(nodes.Items))
	for _, node := range nodes.Items {
		state := strategies.WorkloadNodeState{IsCordon: boolPointer(node.Spec.Unschedulable)}
		if !node.Spec.Unschedulable {
			state.IsDraining = boolPointer(false)
		}
		result[node.Name] = state
	}
	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return result, operationError(err)
	}
	for name, state := range result {
		state.IsDraining = boolPointer(false)
		result[name] = state
	}
	for _, pod := range pods.Items {
		state, ok := result[pod.Spec.NodeName]
		if ok && *state.IsCordon && pod.DeletionTimestamp != nil {
			state.IsDraining = boolPointer(true)
			result[pod.Spec.NodeName] = state
		}
	}
	return result, nil
}

func boolPointer(value bool) *bool { return &value }

func (c *NodeClient) Drain(ctx context.Context, clusterName, nodeName string, options strategies.DrainOptions) error {
	if options.Timeout == 0 {
		options.Timeout = strategies.DefaultDrainTimeout
	}
	if options.Timeout < 0 || options.Timeout > strategies.MaxDrainTimeout {
		return strategies.ErrInvalidDrainOptions
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	err := c.drain(ctx, clusterName, nodeName, options)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return strategies.ErrDrainTimeout
	}
	return err
}

func (c *NodeClient) drain(ctx context.Context, clusterName, nodeName string, options strategies.DrainOptions) error {
	client, err := c.clientFor(ctx, clusterName)
	if err != nil {
		return err
	}
	if err := setCordon(ctx, client, nodeName, true); err != nil {
		return err
	}
	node, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return operationError(err)
	}
	nodeUID := node.UID
	// Track accepted evictions by UID, so a terminating pod is never repeatedly evicted.
	evicted := make(map[types.UID]bool)
	for {
		current, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil {
			return operationError(err)
		}
		if current.UID != nodeUID || !current.Spec.Unschedulable {
			return strategies.ErrNodeOperationConflict
		}
		pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: fields.OneTermEqualSelector("spec.nodeName", nodeName).String()})
		if err != nil {
			return operationError(err)
		}
		pending := make([]corev1.Pod, 0, len(pods.Items))
		// Validate all pods before starting this round of evictions.
		for _, pod := range pods.Items {
			skip, err := skipPod(pod, options)
			if err != nil {
				return err
			}
			if !skip {
				pending = append(pending, pod)
			}
		}
		if len(pending) == 0 {
			return nil
		}
		for _, pod := range pending {
			if pod.DeletionTimestamp != nil || evicted[pod.UID] {
				continue
			}
			uid := pod.UID
			err := client.PolicyV1().Evictions(pod.Namespace).Evict(ctx, &policyv1.Eviction{
				ObjectMeta:    metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace},
				DeleteOptions: &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}},
			})
			switch {
			case err == nil:
				evicted[pod.UID] = true
			case apierrors.IsNotFound(err), apierrors.IsTooManyRequests(err), apierrors.IsConflict(err):
				// Re-list after PDB throttling, concurrent updates or deletion.
			default:
				return operationError(err)
			}
		}
		timer := time.NewTimer(c.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func skipPod(pod corev1.Pod, options strategies.DrainOptions) (bool, error) {
	if _, mirror := pod.Annotations[corev1.MirrorPodAnnotationKey]; mirror {
		return true, nil
	}
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return true, nil
	}
	owner := metav1.GetControllerOf(&pod)
	if owner != nil && owner.Kind == daemonSetKind && owner.APIVersion == "apps/v1" {
		if options.IgnoreDaemonSets {
			return true, nil
		}
		return false, fmt.Errorf("%w: DaemonSet pod %s/%s; use ignore_daemon_sets", strategies.ErrDrainBlocked, pod.Namespace, pod.Name)
	}
	if owner == nil && !options.Force {
		return false, fmt.Errorf("%w: unmanaged pod %s/%s; use force", strategies.ErrDrainBlocked, pod.Namespace, pod.Name)
	}
	for _, volume := range pod.Spec.Volumes {
		if volume.EmptyDir != nil && !options.DeleteEmptyDirData {
			return false, fmt.Errorf("%w: pod %s/%s uses emptyDir; use delete_empty_dir_data", strategies.ErrDrainBlocked, pod.Namespace, pod.Name)
		}
	}
	return false, nil
}

func operationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case apierrors.IsNotFound(err):
		return fmt.Errorf("%w: %w", strategies.ErrNodeNotFound, err)
	case apierrors.IsConflict(err):
		return fmt.Errorf("%w: %w", strategies.ErrNodeOperationConflict, err)
	default:
		return fmt.Errorf("%w: %w", strategies.ErrWorkloadUnavailable, err)
	}
}
