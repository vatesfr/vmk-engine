package kubernetes_clients

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type SecretClient struct {
	kubernetesClient client.Client
	namespace        string
}

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
