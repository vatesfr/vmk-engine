package management_kubernetes_client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"capv-proxy/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	kubeconfig       = "apiVersion: v1\nkind: Config"
	kubeconfigSuffix = "-kubeconfig"
	kubeconfigKey    = "value"
	secretDataName   = "my-secret"
	secretDataKey    = "foo"
	secretDataValue  = "bar"
)

func secretWithName(name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Data:       data,
	}
}

func TestSecretClientGet(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		secret := secretWithName(clusterName+kubeconfigSuffix, map[string][]byte{kubeconfigKey: []byte(kubeconfig)})
		secretClient := NewSecretClient(newFakeClient(t, secret), testNamespace)

		config, err := secretClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Equal(t, kubeconfig, config)
	})

	t.Run("empty value", func(t *testing.T) {
		secret := secretWithName(clusterName+kubeconfigSuffix, map[string][]byte{kubeconfigKey: {}})
		secretClient := NewSecretClient(newFakeClient(t, secret), testNamespace)

		config, err := secretClient.Get(t.Context(), clusterName)

		require.NoError(t, err)
		assert.Empty(t, config)
	})

	t.Run("missing value", func(t *testing.T) {
		secret := secretWithName(clusterName+kubeconfigSuffix, map[string][]byte{"other": []byte("x")})
		secretClient := NewSecretClient(newFakeClient(t, secret), testNamespace)

		config, err := secretClient.Get(t.Context(), clusterName)

		assert.Empty(t, config)
		assert.EqualError(t, err, `secret "`+clusterName+kubeconfigSuffix+`" does not contain key "value"`)
	})

	t.Run("error", func(t *testing.T) {
		secretClient := NewSecretClient(newFakeClient(t), testNamespace)

		config, err := secretClient.Get(t.Context(), clusterName)

		assert.Empty(t, config)
		require.Error(t, err)
		assert.ErrorIs(t, err, strategies.ErrClusterNotFound)
	})
}

func TestSecretClientGetSecretData(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		expected := map[string][]byte{secretDataKey: []byte(secretDataValue)}
		secretClient := NewSecretClient(
			newFakeClient(t, secretWithName(secretDataName, expected)), testNamespace,
		)

		data, err := secretClient.GetSecretData(t.Context(), secretDataName)

		require.NoError(t, err)
		assert.Equal(t, expected, data)
	})

	t.Run("nil data", func(t *testing.T) {
		secretClient := NewSecretClient(
			newFakeClient(t, secretWithName(secretDataName, nil)), testNamespace,
		)

		data, err := secretClient.GetSecretData(t.Context(), secretDataName)

		require.NoError(t, err)
		assert.Nil(t, data)
	})

	t.Run("error", func(t *testing.T) {
		secretClient := NewSecretClient(newFakeClient(t), testNamespace)

		data, err := secretClient.GetSecretData(t.Context(), secretDataName)

		assert.Nil(t, data)
		require.Error(t, err)
		assert.ErrorIs(t, err, strategies.ErrClusterNotFound)
	})

	t.Run("kubernetes error", func(t *testing.T) {
		secretClient := NewSecretClient(
			newErrorClient(t, interceptor.Funcs{Get: getErrorFunc(t, errTest)}), testNamespace,
		)

		data, err := secretClient.GetSecretData(t.Context(), secretDataName)

		assert.Nil(t, data)
		assert.ErrorIs(t, err, errTest)
	})
}
