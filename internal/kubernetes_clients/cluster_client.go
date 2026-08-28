package kubernetes_clients

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ClusterClient struct {
	kubernetesClient client.Client
	namespace        string
}

func NewClusterClient(kubernetesClient client.Client, namespace string) ClusterClient {
	return ClusterClient{kubernetesClient, namespace}
}

type CreateClusterParams struct {
	Name        string
	Labels      map[string]string
	Annotations map[string]string
	Spec        capiv1.ClusterSpec
}

func (c ClusterClient) CreateCluster(params CreateClusterParams) (*capiv1.Cluster, error) {
	if params.Name == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if c.namespace == "" {
		return nil, fmt.Errorf("cluster namespace is required")
	}

	cluster := &capiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        params.Name,
			Namespace:   c.namespace,
			Labels:      params.Labels,
			Annotations: params.Annotations,
		},
		Spec: params.Spec,
	}

	if err := c.kubernetesClient.Create(context.Background(), cluster); err != nil {
		return nil, fmt.Errorf("create Cluster %s/%s: %w", c.namespace, params.Name, err)
	}

	return cluster, nil
}

func (c ClusterClient) GetClusters() (*capiv1.ClusterList, error) {
	clusters := &capiv1.ClusterList{}
	if err := c.kubernetesClient.List(
		context.Background(),
		clusters,
		client.InNamespace(c.namespace),
	); err != nil {
		return nil, err
	}

	return clusters, nil
}

func (c ClusterClient) GetCluster(name string) (*capiv1.Cluster, error) {
	cluster := &capiv1.Cluster{}
	if err := c.kubernetesClient.Get(
		context.Background(),
		client.ObjectKey{Namespace: c.namespace, Name: name},
		cluster,
	); err != nil {
		return nil, err
	}

	return cluster, nil
}

func (c ClusterClient) DeleteCluster(name string) error {
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
