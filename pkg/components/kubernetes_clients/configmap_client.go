package kubernetes_clients

import (
	"context"
	"fmt"
	"strconv"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
)

const ClusterDefaultsConfigMapName = "cluster-defaults"

type ConfigMapClient struct {
	kubernetesClient client.Client
	namespace        string
}

var _ strategies.ForGettingClusterCreationDefaults = ConfigMapClient{}

func NewConfigMapClient(kubernetesClient client.Client, namespace string) ConfigMapClient {
	return ConfigMapClient{kubernetesClient: kubernetesClient, namespace: namespace}
}

func (c ConfigMapClient) GetClusterCreationDefaults(ctx context.Context) (strategies.ClusterCreationDefaults, error) {
	configMap := &corev1.ConfigMap{}
	if err := c.kubernetesClient.Get(ctx, client.ObjectKey{
		Namespace: c.namespace, Name: ClusterDefaultsConfigMapName,
	}, configMap); err != nil {
		return strategies.ClusterCreationDefaults{}, fmt.Errorf("get cluster defaults ConfigMap: %w", err)
	}

	defaults := strategies.ClusterCreationDefaults{}
	for _, field := range []struct {
		key    string
		target *string
	}{
		{"clusterClassName", &defaults.ClusterClassName},
		{"kubernetesVersion", &defaults.KubernetesVersion},
		{"workerName", &defaults.WorkerName},
		{"workerClass", &defaults.WorkerClass},
		{"controlPlaneLoadBalancer", &defaults.ControlPlaneLoadBalancer},
	} {
		value, ok := configMap.Data[field.key]
		if !ok || value == "" {
			return strategies.ClusterCreationDefaults{}, missingConfigMapKeyError(field.key)
		}
		*field.target = value
	}
	for _, field := range []struct {
		key      string
		target   *int
		min, max int
	}{
		{"controlPlaneEndpointPort", &defaults.ControlPlaneEndpointPort, 1, 65535},
		{"controlPlaneEndpointSubnet", &defaults.ControlPlaneEndpointSubnet, 0, 32},
	} {
		value, ok := configMap.Data[field.key]
		if !ok {
			return strategies.ClusterCreationDefaults{}, missingConfigMapKeyError(field.key)
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < field.min || parsed > field.max {
			return strategies.ClusterCreationDefaults{}, fmt.Errorf(
				"ConfigMap %q key %q must be an integer between %d and %d, got %q",
				ClusterDefaultsConfigMapName, field.key, field.min, field.max, value,
			)
		}
		*field.target = parsed
	}
	return defaults, nil
}

func missingConfigMapKeyError(key string) error {
	return fmt.Errorf("ConfigMap %q is missing required key %q", ClusterDefaultsConfigMapName, key)
}
