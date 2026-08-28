package adapters

import (
	"fmt"

	"capv-proxy/internal/kubernetes_clients"
	"capv-proxy/internal/webserver/strategies"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type K8sHTTPAdapter struct {
	clusterClient kubernetes_clients.ClusterClient
	machineClient kubernetes_clients.MachineClient
	secretClient  kubernetes_clients.SecretClient
}

func NewK8sHTTPAdapter(clusterClient kubernetes_clients.ClusterClient, machineClient kubernetes_clients.MachineClient, secretClient kubernetes_clients.SecretClient) K8sHTTPAdapter {
	return K8sHTTPAdapter{clusterClient, machineClient, secretClient}
}

func (a K8sHTTPAdapter) CreateCluster(params strategies.ClusterCreateParams) (strategies.ClusterInfo, error) {
	cluster, err := a.clusterClient.CreateCluster(kubernetes_clients.CreateClusterParams{
		Name: params.Name,
	})
	if err != nil {
		return strategies.ClusterInfo{}, err
	}

	return toClusterInfo(cluster), nil
}

func (a K8sHTTPAdapter) GetClusters() ([]strategies.ClusterInfo, error) {
	clusters, err := a.clusterClient.GetClusters()
	if err != nil {
		return nil, err
	}

	result := make([]strategies.ClusterInfo, 0, len(clusters.Items))
	for i := range clusters.Items {
		result = append(result, toClusterInfo(&clusters.Items[i]))
	}

	return result, nil
}

func (a K8sHTTPAdapter) GetCluster(name string) (strategies.ClusterInfo, error) {
	cluster, err := a.clusterClient.GetCluster(name)
	if err != nil {
		return strategies.ClusterInfo{}, err
	}

	return toClusterInfo(cluster), nil
}

func (a K8sHTTPAdapter) GetClusterNodesInfo(name string) ([]strategies.ClusterNodeInfo, error) {
	// The Machine-to-Node lookup is not wired yet. Keep this endpoint useful for
	// local API development with deterministic, clearly identifiable mock data.
	return []strategies.ClusterNodeInfo{
		{Name: name + "-control-plane-0", Role: "control-plane", Status: "Ready", Endpoint: "https://192.0.2.10:10250"},
		{Name: name + "-worker-0", Role: "worker", Status: "Ready", Endpoint: "https://192.0.2.11:10250"},
	}, nil
}

func (a K8sHTTPAdapter) GetClusterConfig(name string) (string, error) {
	secretName := name + "-kubeconfig"
	data, err := a.secretClient.GetSecretData(secretName)
	if err != nil {
		return "", err
	}

	value, ok := data["value"]
	if !ok {
		return "", fmt.Errorf("secret %q does not contain key %q", secretName, "value")
	}

	return string(value), nil
}

func (a K8sHTTPAdapter) DeleteCluster(name string) error {
	return a.clusterClient.DeleteCluster(name)
}

func toClusterInfo(cluster *capiv1.Cluster) strategies.ClusterInfo {
	return strategies.ClusterInfo{
		Name:  cluster.GetName(),
		Phase: cluster.Status.Phase,
	}
}
