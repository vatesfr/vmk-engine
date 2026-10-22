package management_kubernetes_client

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"capv-proxy/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/json"
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

	return parseClusterDefaults(configMap.Data)
}

func missingConfigMapKeyError(key string) error {
	return fmt.Errorf("ConfigMap %q is missing required key %q", ClusterDefaultsConfigMapName, key)
}

const (
	workerNameKey                = "workerName"
	podCIDRKey                   = "podCIDR"
	serviceCIDRKey               = "serviceCIDR"
	serviceDomainKey             = "serviceDomain"
	controlPlaneKubeadmConfigKey = "controlPlaneKubeadmConfig"
	controlPlaneMachineSpecKey   = "controlPlaneMachineSpec"
	workerMachineSpecKey         = "workerMachineSpec"
	workerKubeadmConfigKey       = "workerKubeadmConfig"
	cniKey                       = "cni"
	ciliumKey                    = "cilium"
	xoTagsKey                    = "xoTags"
	endpointPortKey              = "controlPlaneEndpointPort"
	endpointSubnetKey            = "controlPlaneEndpointSubnet"
	loadBalancerKey              = "controlPlaneLoadBalancer"
)

func parseClusterDefaults(data map[string]string) (strategies.ClusterCreationDefaults, error) {
	result := strategies.ClusterCreationDefaults{}
	d := &result
	for _, field := range []struct {
		key    string
		target *string
	}{
		{workerNameKey, &result.WorkerName},
		{podCIDRKey, &d.PodCIDR},
		{serviceCIDRKey, &d.ServiceCIDR},
		{serviceDomainKey, &d.ServiceDomain},
		{cniKey, &d.CNI},
	} {
		if strings.TrimSpace(data[field.key]) == "" {
			return result, missingConfigMapKeyError(field.key)
		}
		*field.target = data[field.key]
	}
	for _, field := range []struct {
		key      string
		target   *int
		min, max int
	}{
		{endpointPortKey, &d.EndpointPort, 1, 65535}, {endpointSubnetKey, &d.EndpointSubnet, 1, 32},
	} {
		v, err := strconv.Atoi(data[field.key])
		if err != nil || v < field.min || v > field.max {
			return result, fmt.Errorf("invalid %s: expected integer between %d and %d", field.key, field.min, field.max)
		}
		*field.target = v
	}
	var ok bool
	d.LoadBalancer, ok = data[loadBalancerKey]
	if !ok {
		return result, missingConfigMapKeyError(loadBalancerKey)
	}
	for _, field := range []struct {
		key    string
		target *map[string]any
	}{
		{controlPlaneMachineSpecKey, &d.ControlPlaneMachineSpec},
		{workerMachineSpecKey, &d.WorkerMachineSpec},
		{controlPlaneKubeadmConfigKey, &d.ControlPlaneKubeadmConfig},
		{workerKubeadmConfigKey, &d.WorkerKubeadmConfig},
	} {
		if strings.TrimSpace(data[field.key]) == "" {
			return result, missingConfigMapKeyError(field.key)
		}
		raw, err := yaml.YAMLToJSONStrict([]byte(data[field.key]))
		if err != nil {
			return result, fmt.Errorf("invalid %s: %w", field.key, err)
		}
		if err := json.Unmarshal(raw, field.target); err != nil {
			return result, fmt.Errorf("invalid %s: expected an object: %w", field.key, err)
		}
	}
	for _, field := range []struct {
		key    string
		target any
	}{
		{ciliumKey, &d.Cilium},
		{xoTagsKey, &d.XOTags},
	} {
		if raw := strings.TrimSpace(data[field.key]); raw != "" {
			if err := yaml.UnmarshalStrict([]byte(raw), field.target); err != nil {
				return result, fmt.Errorf("invalid %s: %w", field.key, err)
			}
		}
	}
	return result, validateConfigurationObjects(result)
}

// Check the shape expected by the builder; provider fields are validated by Kubernetes and the provider.
func validateConfigurationObjects(d strategies.ClusterCreationDefaults) error {
	if err := validateProviderOptions(d); err != nil {
		return err
	}
	for _, field := range []struct {
		key   string
		value map[string]any
	}{
		{controlPlaneMachineSpecKey, d.ControlPlaneMachineSpec},
		{workerMachineSpecKey, d.WorkerMachineSpec},
		{controlPlaneKubeadmConfigKey, d.ControlPlaneKubeadmConfig},
		{workerKubeadmConfigKey, d.WorkerKubeadmConfig},
	} {
		if len(field.value) == 0 {
			return fmt.Errorf("%s must be a non-empty configuration object", field.key)
		}
		for _, key := range []string{"apiVersion", "kind", "metadata", "spec"} {
			if _, ok := field.value[key]; ok {
				return fmt.Errorf("%s must contain configuration fields, not a resource manifest", field.key)
			}
		}
	}
	return nil
}

func validateProviderOptions(d strategies.ClusterCreationDefaults) error {
	if len(d.XOTags) > 64 {
		return fmt.Errorf("%s must contain at most 64 tags", xoTagsKey)
	}
	seen := make(map[string]bool, len(d.XOTags))
	for _, tag := range d.XOTags {
		if n := utf8.RuneCountInString(tag); n < 1 || n > 255 {
			return fmt.Errorf("%s tags must contain 1 to 255 characters", xoTagsKey)
		}
		if seen[tag] {
			return fmt.Errorf("%s must contain unique tags", xoTagsKey)
		}
		seen[tag] = true
	}
	if d.Cilium == nil {
		return nil
	}
	c := d.Cilium
	if c.KubeProxyReplacement && d.CNI != ciliumKey {
		return fmt.Errorf("%s.kubeProxyReplacement requires cni to be cilium", ciliumKey)
	}
	if (c.K8sServiceHost != "") != (c.K8sServicePort != 0) {
		return fmt.Errorf("%s.k8sServiceHost and k8sServicePort must be set together", ciliumKey)
	}
	if c.K8sServiceHost != "" && !c.KubeProxyReplacement {
		return fmt.Errorf("%s.k8sServiceHost and k8sServicePort require kubeProxyReplacement", ciliumKey)
	}
	if c.K8sServicePort < 0 || c.K8sServicePort > 65535 {
		return fmt.Errorf("%s.k8sServicePort must be between 1 and 65535 when set", ciliumKey)
	}
	return nil
}
