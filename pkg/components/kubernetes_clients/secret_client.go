package kubernetes_clients

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %w", strategies.ErrClusterNotFound, err)
		}
		return nil, err
	}

	return kubernetesSecret.Data, nil
}
