package kubernetes_clients

import (
	"context"
	"fmt"

	"capv-proxy/internal/strategies"
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

func (c ClusterClient) Create(params strategies.CreateClusterResourceParams) (strategies.ClusterResourceInfo, error) {
	if params.Name == "" {
		return strategies.ClusterResourceInfo{}, fmt.Errorf("cluster name is required")
	}
	if c.namespace == "" {
		return strategies.ClusterResourceInfo{}, fmt.Errorf("cluster namespace is required")
	}

	clusterResource := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.Name,
			Namespace: c.namespace,
		},
	}

	if err := c.kubernetesClient.Create(context.Background(), clusterResource); err != nil {
		return strategies.ClusterResourceInfo{}, fmt.Errorf("create Cluster %s/%s: %w", c.namespace, params.Name, err)
	}

	return toInfo(clusterResource), nil
}

func (c ClusterClient) List() ([]strategies.ClusterResourceInfo, error) {
	clusters := &capiv1.ClusterList{}
	if err := c.kubernetesClient.List(
		context.Background(),
		clusters,
		client.InNamespace(c.namespace),
	); err != nil {
		return nil, err
	}

	result := make([]strategies.ClusterResourceInfo, 0, len(clusters.Items))
	for i := range clusters.Items {
		result = append(result, toInfo(&clusters.Items[i]))
	}
	return result, nil
}

func (c ClusterClient) Get(name string) (strategies.ClusterResourceInfo, error) {
	clusterResource := &capiv1.Cluster{}
	if err := c.kubernetesClient.Get(
		context.Background(),
		client.ObjectKey{Namespace: c.namespace, Name: name},
		clusterResource,
	); err != nil {
		return strategies.ClusterResourceInfo{}, err
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

func toInfo(value *capiv1.Cluster) strategies.ClusterResourceInfo {
	return strategies.ClusterResourceInfo{Name: value.GetName(), Phase: value.Status.Phase}
}
