package management_kubernetes_client

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/helpers/uuids"
	"capv-proxy/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	toolscache "k8s.io/client-go/tools/cache"
)

const (
	eventResourceUID   = "event-uid"
	secretExampleValue = "private-key-and-token"
)

type eventInformer struct {
	cache.Informer
	handler  toolscache.ResourceEventHandler
	removed  int
	addError error
}

func (i *eventInformer) AddEventHandler(handler toolscache.ResourceEventHandler) (toolscache.ResourceEventHandlerRegistration, error) {
	if i.addError != nil {
		return nil, i.addError
	}
	i.handler = handler
	return nil, nil
}

func (i *eventInformer) RemoveEventHandler(toolscache.ResourceEventHandlerRegistration) error {
	i.removed++
	return nil
}

type eventCache struct {
	cache.Cache
	clusters, machines, secrets *eventInformer
}

func (c eventCache) GetInformer(_ context.Context, object client.Object, _ ...cache.InformerGetOption) (cache.Informer, error) {
	switch object.(type) {
	case *capiv1.Cluster:
		return c.clusters, nil
	case *capiv1.Machine:
		return c.machines, nil
	case *corev1.Secret:
		return c.secrets, nil
	default:
		return nil, errors.New("unexpected informer")
	}
}

func newEventCache(t *testing.T, objects ...client.Object) eventCache {
	t.Helper()
	return eventCache{Cache: newFakeCache(t, objects...), clusters: &eventInformer{}, machines: &eventInformer{}, secrets: &eventInformer{}}
}

func TestObserveResourceChangesAndCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		informerCache := newEventCache(t)
		var changes []strategies.KubernetesResourceChange
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stop, err := NewEventClient(informerCache, testNamespace).ObserveKubernetesResources(ctx, func(change strategies.KubernetesResourceChange) { changes = append(changes, change) })
		require.NoError(t, err)
		cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace, UID: eventResourceUID}}
		informerCache.clusters.handler.OnAdd(cluster, true)
		assert.Empty(t, changes, "initial resources do not generate creation notifications")
		time.Sleep(2 * eventInformerSyncTimeout)
		informerCache.clusters.handler.OnAdd(cluster, false)
		require.Len(t, changes, 1)
		assert.Nil(t, changes[0].Previous)
		assert.Equal(t, strategies.ObservedCluster, changes[0].Current.Kind)
		next := cluster.DeepCopy()
		next.Spec.Paused = new(true)
		informerCache.clusters.handler.OnUpdate(cluster, next)
		require.Len(t, changes, 2)
		assert.False(t, changes[1].Previous.Cluster.Paused)
		assert.True(t, changes[1].Current.Cluster.Paused)
		informerCache.clusters.handler.OnDelete(toolscache.DeletedFinalStateUnknown{Obj: next})
		require.Len(t, changes, 3)
		assert.Equal(t, eventResourceUID, changes[2].Previous.UID)
		assert.Nil(t, changes[2].Current)
		foreign := cluster.DeepCopy()
		foreign.Namespace = "unrelated"
		informerCache.clusters.handler.OnAdd(foreign, false)
		assert.Len(t, changes, 3)
		cancel()
		synctest.Wait()
		stop()
		assert.Equal(t, 1, informerCache.clusters.removed)
		assert.Equal(t, 1, informerCache.machines.removed)
		assert.Equal(t, 1, informerCache.secrets.removed)
		informerCache.clusters.handler.OnAdd(cluster, false)
		assert.Len(t, changes, 3, "stopped observers ignore pending callbacks")
	})
}

func TestKubeconfigAndClusterArrivalOrder(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		secretFirst, initialCluster, initialSecret bool
	}{
		{name: "cluster then secret"},
		{name: "secret then cluster", secretFirst: true},
		{name: "initial secret then new cluster", secretFirst: true, initialSecret: true},
		{name: "new cluster then initial secret", initialSecret: true},
		{name: "initial cluster then live secret", initialCluster: true},
		{name: "live secret then initial cluster", secretFirst: true, initialCluster: true},
		{name: "initial cluster then initial secret", initialCluster: true, initialSecret: true},
		{name: "initial secret then initial cluster", secretFirst: true, initialCluster: true, initialSecret: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			informerCache := newEventCache(t)
			// The join must use informer callbacks, not fresh Cluster reads that
			// can overtake queued callbacks and introduce out-of-order snapshots.
			informerCache.Cache = nil
			var changes []strategies.KubernetesResourceChange
			stop, err := NewEventClient(informerCache, testNamespace).ObserveKubernetesResources(context.Background(), func(change strategies.KubernetesResourceChange) {
				changes = append(changes, change)
			})
			require.NoError(t, err)
			defer stop()
			cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace, UID: eventResourceUID}}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: clusterName + "-kubeconfig", Namespace: testNamespace},
				Data:       map[string][]byte{kubeconfigKey: []byte(secretExampleValue)},
			}
			addCluster := func() { informerCache.clusters.handler.OnAdd(cluster, tc.initialCluster) }
			addSecret := func() { informerCache.secrets.handler.OnAdd(secret, tc.initialSecret) }
			if tc.secretFirst {
				addSecret()
				assert.Empty(t, changes, "wait until Cluster identity is known")
				addCluster()
			} else {
				addCluster()
				addSecret()
			}
			var available []*strategies.KubernetesResourceObservation
			for _, change := range changes {
				if change.Current.Kind == strategies.ObservedKubeconfig {
					available = append(available, change.Current)
				}
			}
			if tc.initialCluster && tc.initialSecret {
				assert.Empty(t, changes)
				// Initial availability must still seed the baseline for later removal.
				informerCache.secrets.handler.OnDelete(secret)
				require.Len(t, changes, 1)
				assert.True(t, changes[0].Previous.KubeconfigAvailable)
				assert.False(t, changes[0].Current.KubeconfigAvailable)
				return
			}
			require.Len(t, available, 1)
			assert.True(t, available[0].KubeconfigAvailable)
			assert.Equal(t, eventResourceUID, available[0].UID)
			assert.Equal(t, uuids.ClusterID(clusterName), available[0].ID)
			assert.Nil(t, available[0].Cluster, "kubeconfig events carry no mutable Cluster snapshot")
			assert.Nil(t, available[0].Node)
			if !tc.initialCluster {
				assert.Equal(t, strategies.ObservedCluster, changes[0].Current.Kind, "creation precedes availability")
			}
			count := len(changes)
			informerCache.secrets.handler.OnUpdate(secret, secret.DeepCopy())
			assert.Len(t, changes, count, "rotation without availability change emits no event")
		})
	}
}

func TestKubeconfigDeletionAndClusterReplacement(t *testing.T) {
	informerCache := newEventCache(t)
	var changes []strategies.KubernetesResourceChange
	stop, err := NewEventClient(informerCache, testNamespace).ObserveKubernetesResources(context.Background(), func(change strategies.KubernetesResourceChange) {
		changes = append(changes, change)
	})
	require.NoError(t, err)
	defer stop()
	cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace, UID: eventResourceUID}}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName + "-kubeconfig", Namespace: testNamespace,
			OwnerReferences: []metav1.OwnerReference{{Kind: strategies.ObservedCluster, Name: clusterName, UID: cluster.UID}},
		},
		Data: map[string][]byte{kubeconfigKey: []byte(secretExampleValue)},
	}
	informerCache.secrets.handler.OnAdd(secret, false)
	informerCache.secrets.handler.OnDelete(toolscache.DeletedFinalStateUnknown{Obj: secret})
	informerCache.clusters.handler.OnAdd(cluster, false)
	require.Len(t, changes, 1, "a pending Secret deleted before its Cluster must not announce availability")
	informerCache.secrets.handler.OnAdd(secret, false)
	require.Len(t, changes, 2)
	assert.True(t, changes[1].Current.KubeconfigAvailable)
	replacement := cluster.DeepCopy()
	replacement.UID = "replacement"
	informerCache.clusters.handler.OnUpdate(cluster, replacement)
	require.Len(t, changes, 3, "a Secret owned by the old Cluster is not available for its replacement")
	replacedSecret := secret.DeepCopy()
	replacedSecret.OwnerReferences[0].UID = replacement.UID
	informerCache.secrets.handler.OnUpdate(secret, replacedSecret)
	require.Len(t, changes, 4)
	assert.Equal(t, string(replacement.UID), changes[3].Current.UID)
	assert.True(t, changes[3].Current.KubeconfigAvailable)
	informerCache.clusters.handler.OnDelete(replacement)
	informerCache.secrets.handler.OnDelete(replacedSecret)
	require.Len(t, changes, 5, "late Secret callbacks must not resurrect a deleted Cluster")
}

type unsyncedSecretCache struct{ eventCache }

func (c unsyncedSecretCache) GetInformer(ctx context.Context, object client.Object, options ...cache.InformerGetOption) (cache.Informer, error) {
	if _, secret := object.(*corev1.Secret); secret {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return c.eventCache.GetInformer(ctx, object, options...)
}

func TestObserverSyncTimeoutRollsBackRegistrations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		informerCache := unsyncedSecretCache{newEventCache(t)}
		started := time.Now()
		stop, err := NewEventClient(informerCache, testNamespace).ObserveKubernetesResources(context.Background(), func(strategies.KubernetesResourceChange) {})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Contains(t, err.Error(), "Secret")
		assert.Equal(t, eventInformerSyncTimeout, time.Since(started))
		assert.Nil(t, stop)
		assert.Equal(t, 1, informerCache.clusters.removed)
		assert.Equal(t, 1, informerCache.machines.removed)
	})
}

func TestObserverRegistrationRollsBackOnFailure(t *testing.T) {
	informerCache := newEventCache(t)
	informerCache.machines.addError = errors.New("registration rejected")
	stop, err := NewEventClient(informerCache, testNamespace).ObserveKubernetesResources(context.Background(), func(strategies.KubernetesResourceChange) {})
	require.Error(t, err)
	assert.Nil(t, stop)
	assert.Equal(t, 1, informerCache.clusters.removed)
	assert.Zero(t, informerCache.secrets.removed)
}

func TestKubeconfigObservationNeverIncludesSecretData(t *testing.T) {
	cluster := &capiv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: testNamespace, UID: eventResourceUID}}
	source := NewEventClient(newEventCache(t, cluster), testNamespace)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: clusterName + "-kubeconfig", Namespace: testNamespace}, Data: map[string][]byte{kubeconfigKey: []byte(secretExampleValue)}}
	observation := source.kubeconfigObservation(secret)
	require.NotNil(t, observation)
	assert.True(t, observation.KubeconfigAvailable)
	assert.Equal(t, clusterName, observation.Name)
	assert.Empty(t, observation.UID, "Cluster identity is resolved by the observer coordinator")
	assert.Nil(t, observation.Cluster)
	assert.Equal(t, strategies.ObservedKubeconfig, observation.Kind)
	encoded, err := json.Marshal(observation)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), secretExampleValue)
	secret.Data = nil
	assert.False(t, source.kubeconfigObservation(secret).KubeconfigAvailable)
	secret.Name = "provider-credentials"
	assert.Nil(t, source.kubeconfigObservation(secret))
	secret.Name = "absent-cluster-kubeconfig"
	assert.NotNil(t, source.kubeconfigObservation(secret), "retain availability until its Cluster is observed")
}
