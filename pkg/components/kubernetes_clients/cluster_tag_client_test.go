package kubernetes_clients_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	"capv-proxy/pkg/components/kubernetes_clients"
	"capv-proxy/pkg/strategies"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const testTagLabelPrefix = "tags.vmk.vates.tech/"

const (
	ownerTag      = "owner"
	platformValue = "platform"
)

func TestClusterClientCreatesClusterWithTags(t *testing.T) {
	kubeClient := newFakeClient(t)
	clusterClient := kubernetes_clients.NewClusterClient(kubeClient, testNamespace)

	_, err := clusterClient.Create(t.Context(), strategies.CreateClusterResourceParams{
		Name: clusterName,
		Tags: map[string]string{"environment": "production", ownerTag: platformValue},
	})

	require.NoError(t, err)
	stored := &capiv1.Cluster{}
	require.NoError(t, kubeClient.Get(context.Background(), types.NamespacedName{
		Namespace: testNamespace,
		Name:      clusterName,
	}, stored))
	assert.Equal(t, map[string]string{
		testTagLabelPrefix + "environment": "production",
		testTagLabelPrefix + ownerTag:      platformValue,
	}, stored.Labels)
}

func TestClusterClientManagesClusterTags(t *testing.T) {
	cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name:      clusterName,
		Namespace: testNamespace,
		Labels: map[string]string{
			"cluster.x-k8s.io/cluster-name": clusterName,
			testTagLabelPrefix + ownerTag:   platformValue,
		},
	}}
	kubeClient := newFakeClient(t, cluster)
	clusterClient := kubernetes_clients.NewClusterClient(kubeClient, testNamespace)

	tags, err := clusterClient.GetTags(t.Context(), clusterName)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{ownerTag: platformValue}, tags)

	require.NoError(t, clusterClient.SetTag(t.Context(), clusterName, ownerTag, "compute"))
	require.NoError(t, clusterClient.SetTag(t.Context(), clusterName, "empty", ""))
	value, err := clusterClient.GetTag(t.Context(), clusterName, ownerTag)
	require.NoError(t, err)
	assert.Equal(t, "compute", value)

	require.NoError(t, clusterClient.DeleteTag(t.Context(), clusterName, ownerTag))
	_, err = clusterClient.GetTag(t.Context(), clusterName, ownerTag)
	assert.ErrorIs(t, err, strategies.ErrClusterTagNotFound)

	stored := &capiv1.Cluster{}
	require.NoError(t, kubeClient.Get(t.Context(), types.NamespacedName{
		Namespace: testNamespace,
		Name:      clusterName,
	}, stored))
	assert.Equal(t, clusterName, stored.Labels["cluster.x-k8s.io/cluster-name"])
	assert.Equal(t, "", stored.Labels[testTagLabelPrefix+"empty"])
}

func TestClusterClientTagErrors(t *testing.T) {
	clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), testNamespace)

	_, err := clusterClient.GetTags(t.Context(), clusterName)
	assert.ErrorIs(t, err, strategies.ErrClusterNotFound)

	cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace}}
	clusterClient = kubernetes_clients.NewClusterClient(newFakeClient(t, cluster), testNamespace)
	err = clusterClient.DeleteTag(t.Context(), clusterName, "missing")
	assert.ErrorIs(t, err, strategies.ErrClusterTagNotFound)
}

func TestClusterClientValidatesTagsAsKubernetesLabels(t *testing.T) {
	clusterClient := kubernetes_clients.NewClusterClient(newFakeClient(t), testNamespace)

	_, err := clusterClient.Create(t.Context(), strategies.CreateClusterResourceParams{
		Name: clusterName,
		Tags: map[string]string{strings.Repeat("a", 64): "value"},
	})
	assert.ErrorIs(t, err, strategies.ErrInvalidClusterTagName)

	err = clusterClient.SetTag(t.Context(), clusterName, "valid", strings.Repeat("a", 64))
	assert.ErrorIs(t, err, strategies.ErrInvalidClusterTagValue)

	err = clusterClient.SetTag(t.Context(), clusterName, "invalid/name", "value")
	assert.ErrorIs(t, err, strategies.ErrInvalidClusterTagName)
}
