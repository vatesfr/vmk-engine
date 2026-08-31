package kubernetes_clients

import (
	"context"
	"fmt"

	"capv-proxy/internal/strategies"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (c SecretClient) Get(clusterName string) (string, error) {
	secretName := clusterName + "-kubeconfig"
	data, err := c.GetSecretData(secretName)
	if err != nil {
		return "", err
	}

	value, ok := data["value"]
	if !ok {
		return "", fmt.Errorf("secret %q does not contain key %q", secretName, "value")
	}
	return string(value), nil
}

type SecretClient struct {
	kubernetesClient client.Client
	namespace        string
}

var _ strategies.ForGettingClusterConfig = SecretClient{}

type Secret struct {
	key, value string
}

func NewSecretClient(kubernetesClient client.Client, namespace string) SecretClient {
	return SecretClient{kubernetesClient, namespace}
}

func (s SecretClient) GetSecretData(name string) (map[string][]byte, error) {
	kubernetesSecret := &corev1.Secret{}
	if err := s.kubernetesClient.Get(
		context.TODO(),
		client.ObjectKey{Namespace: s.namespace, Name: name},
		kubernetesSecret,
	); err != nil {
		return nil, err
	}

	return kubernetesSecret.Data, nil
}
