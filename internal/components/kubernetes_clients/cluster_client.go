package kubernetes_clients

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"capv-proxy/internal/strategies"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ClusterClient struct {
	kubernetesClient client.Client
	namespace        string
}

var _ strategies.ForManagingClusterResources = ClusterClient{}

func NewClusterClient(kubernetesClient client.Client, namespace string) ClusterClient {
	return ClusterClient{kubernetesClient, namespace}
}

func (c ClusterClient) Create(params strategies.CreateClusterResourceParams) (strategies.ClusterInfo, error) {
	if params.Name == "" {
		return strategies.ClusterInfo{}, fmt.Errorf("cluster name is required")
	}
	if c.namespace == "" {
		return strategies.ClusterInfo{}, fmt.Errorf("cluster namespace is required")
	}

	clusterResource := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.Name,
			Namespace: c.namespace,
		},
		Spec: capiv1.ClusterSpec{
			Topology: capiv1.Topology{
				ClassRef: capiv1.ClusterClassRef{
					Name: params.ClusterClassName,
				},
				Version: params.KubernetesVersion,
				ControlPlane: capiv1.ControlPlaneTopology{
					Replicas: params.ControlPlaneReplicas,
				},
				Workers: capiv1.WorkersTopology{
					MachineDeployments: toMachineDeployments(params.MachineDeployments),
				},
			},
		},
	}

	variables, err := toClusterVariables(params.Variables)
	if err != nil {
		return strategies.ClusterInfo{}, err
	}
	clusterResource.Spec.Topology.Variables = variables

	if err := c.kubernetesClient.Create(context.Background(), clusterResource); err != nil {
		return strategies.ClusterInfo{}, fmt.Errorf("create Cluster %s/%s: %w", c.namespace, params.Name, err)
	}

	return strategies.ClusterInfo{Name: clusterResource.GetName(), Phase: "Creating"}, nil
}

func toMachineDeployments(machineDeployments []strategies.MachineDeployment) []capiv1.MachineDeploymentTopology {
	result := make([]capiv1.MachineDeploymentTopology, 0, len(machineDeployments))
	for _, machineDeployment := range machineDeployments {
		result = append(result, capiv1.MachineDeploymentTopology{
			Name:          machineDeployment.Name,
			Class:         machineDeployment.Class,
			Replicas:      machineDeployment.Replicas,
			FailureDomain: machineDeployment.FailureDomain,
		})
	}
	return result
}

func toClusterVariables(variables map[string]any) ([]capiv1.ClusterVariable, error) {
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]capiv1.ClusterVariable, 0, len(names))
	for _, name := range names {
		value, err := json.Marshal(variables[name])
		if err != nil {
			return nil, fmt.Errorf("encode cluster variable %q: %w", name, err)
		}
		result = append(result, capiv1.ClusterVariable{
			Name:  name,
			Value: apiextensionsv1.JSON{Raw: value},
		})
	}
	return result, nil
}

func (c ClusterClient) List() ([]strategies.ClusterInfo, error) {
	clusters := &capiv1.ClusterList{}
	if err := c.kubernetesClient.List(
		context.Background(),
		clusters,
		client.InNamespace(c.namespace),
	); err != nil {
		return nil, err
	}

	result := make([]strategies.ClusterInfo, 0, len(clusters.Items))
	for i := range clusters.Items {
		result = append(result, toInfo(&clusters.Items[i]))
	}
	return result, nil
}

func (c ClusterClient) Get(name string) (strategies.ClusterInfo, error) {
	clusterResource := &capiv1.Cluster{}
	if err := c.kubernetesClient.Get(
		context.Background(),
		client.ObjectKey{Namespace: c.namespace, Name: name},
		clusterResource,
	); err != nil {
		return strategies.ClusterInfo{}, err
	}

	return toInfo(clusterResource), nil
}

func (c ClusterClient) Delete(name string) error {
	if name == "" {
		return fmt.Errorf("cluster name is required")
	}
	if c.namespace == "" {
		return fmt.Errorf("cluster namespace is required")
	}

	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
		},
	}

	if err := c.kubernetesClient.Delete(context.Background(), cluster); err != nil {
		return fmt.Errorf("delete Cluster %s/%s: %w", c.namespace, name, err)
	}

	return nil
}

func toInfo(value *capiv1.Cluster) strategies.ClusterInfo {
	controlPlaneEndpoint := ""
	if value.Spec.ControlPlaneEndpoint.IsValid() {
		controlPlaneEndpoint = "https://" + value.Spec.ControlPlaneEndpoint.String()
	}

	return strategies.ClusterInfo{
		Name:                 value.GetName(),
		Phase:                value.Status.Phase,
		ControlPlaneEndpoint: controlPlaneEndpoint,
	}
}
