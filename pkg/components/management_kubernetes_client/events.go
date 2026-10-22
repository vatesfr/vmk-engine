package management_kubernetes_client

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"capv-proxy/pkg/helpers/uuids"
	"capv-proxy/pkg/strategies"

	corev1 "k8s.io/api/core/v1"
	toolscache "k8s.io/client-go/tools/cache"
)

const eventInformerSyncTimeout = 30 * time.Second

// EventClient reuses the namespace-scoped informer cache. It never creates a
// watch per HTTP connection and never exposes Secret contents.
type EventClient struct {
	cache     cache.Cache
	namespace string
}

var _ strategies.ForObservingKubernetesResources = EventClient{}

func NewEventClient(informerCache cache.Cache, namespace string) EventClient {
	return EventClient{cache: informerCache, namespace: namespace}
}

func (c EventClient) ObserveKubernetesResources(ctx context.Context, handle func(strategies.KubernetesResourceChange)) (func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	var once sync.Once
	var removers []func()
	stop := func() {
		once.Do(func() {
			cancel()
			for _, remove := range removers {
				remove()
			}
		})
	}
	notify := func(change strategies.KubernetesResourceChange) {
		if ctx.Err() == nil && !change.Initial {
			handle(change)
		}
	}
	kubeconfigs := kubeconfigEvents{
		clusters: make(map[client.ObjectKey]kubeconfigCluster),
		secrets:  make(map[client.ObjectKey]kubeconfigState),
		observed: make(map[client.ObjectKey]*strategies.KubernetesResourceObservation),
	}

	for _, observe := range []func(context.Context, func(strategies.KubernetesResourceChange)) (func(), error){
		func(ctx context.Context, notify func(strategies.KubernetesResourceChange)) (func(), error) {
			return c.observeClusters(ctx, func(change strategies.KubernetesResourceChange) { kubeconfigs.clusterChanged(change, notify) })
		},
		c.observeMachines,
		func(ctx context.Context, notify func(strategies.KubernetesResourceChange)) (func(), error) {
			return c.observeKubeconfigs(ctx, func(change strategies.KubernetesResourceChange) { kubeconfigs.secretChanged(change, notify) })
		},
	} {
		remove, err := observe(ctx, notify)
		if err != nil {
			stop()
			return nil, err
		}
		removers = append(removers, remove)
	}

	go func() {
		<-ctx.Done()
		stop()
	}()
	return stop, nil
}

// observeResource shares only informer registration, namespace filtering,
// initial-list tracking and tombstone handling. Resource projections are typed.
func observeResource[T client.Object](
	ctx context.Context, c EventClient, object T,
	project func(T) *strategies.KubernetesResourceObservation,
	handle func(strategies.KubernetesResourceChange),
) (func(), error) {
	syncCtx, cancelSync := context.WithTimeout(ctx, eventInformerSyncTimeout)
	informer, err := c.cache.GetInformer(syncCtx, object)
	cancelSync()
	if err != nil {
		return nil, fmt.Errorf("get event informer for %T: %w", object, err)
	}
	observation := func(value any) *strategies.KubernetesResourceObservation {
		if tombstone, ok := value.(toolscache.DeletedFinalStateUnknown); ok {
			value = tombstone.Obj
		}
		resource, ok := value.(T)
		if !ok || resource.GetNamespace() != c.namespace {
			return nil
		}
		return project(resource)
	}
	notify := func(previous, current any, initial bool) {
		if ctx.Err() != nil {
			return
		}
		before, after := observation(previous), observation(current)
		if before != nil || after != nil {
			handle(strategies.KubernetesResourceChange{Previous: before, Current: after, Initial: initial})
		}
	}
	registration, err := informer.AddEventHandler(toolscache.ResourceEventHandlerDetailedFuncs{
		AddFunc:    func(obj any, initial bool) { notify(nil, obj, initial) },
		UpdateFunc: func(oldObj, newObj any) { notify(oldObj, newObj, false) },
		DeleteFunc: func(obj any) { notify(obj, nil, false) },
	})
	if err != nil {
		return nil, fmt.Errorf("register event observer for %T: %w", object, err)
	}
	return func() {
		if err := informer.RemoveEventHandler(registration); err != nil {
			slog.ErrorContext(ctx, "remove Kubernetes event observer", "error", err)
		}
	}, nil
}

func resourceObservation(object client.Object) *strategies.KubernetesResourceObservation {
	return &strategies.KubernetesResourceObservation{
		Namespace: object.GetNamespace(), Name: object.GetName(), UID: string(object.GetUID()),
		Deleting: !object.GetDeletionTimestamp().IsZero(),
	}
}

func (c EventClient) observeClusters(ctx context.Context, handle func(strategies.KubernetesResourceChange)) (func(), error) {
	return observeResource(ctx, c, &capiv1.Cluster{}, c.clusterObservation, handle)
}

func (c EventClient) clusterObservation(cluster *capiv1.Cluster) *strategies.KubernetesResourceObservation {
	observation := resourceObservation(cluster)
	info := toInfo(cluster)
	observation.ID = info.ID
	observation.Kind, observation.Cluster = strategies.ObservedCluster, &info
	observation.Scaling = clusterScaling(cluster)
	// Malformed/missing operation state must never imply completion.
	observation.Upgrade, _ = readUpgrade(cluster)
	return observation
}

func (c EventClient) observeMachines(ctx context.Context, handle func(strategies.KubernetesResourceChange)) (func(), error) {
	return observeResource(ctx, c, &capiv1.Machine{}, c.machineObservation, handle)
}

func (c EventClient) machineObservation(machine *capiv1.Machine) *strategies.KubernetesResourceObservation {
	observation := resourceObservation(machine)
	info := toClusterNodeInfo(machine)
	observation.ID = info.ID
	observation.Kind, observation.Node = strategies.ObservedNode, &info
	observation.ClusterName = machine.Spec.ClusterName
	return observation
}

func (c EventClient) observeKubeconfigs(ctx context.Context, handle func(strategies.KubernetesResourceChange)) (func(), error) {
	return observeResource(ctx, c, &corev1.Secret{}, c.kubeconfigObservation, handle)
}

func (c EventClient) kubeconfigObservation(secret *corev1.Secret) *strategies.KubernetesResourceObservation {
	const suffix = "-kubeconfig"
	if !strings.HasSuffix(secret.Name, suffix) {
		return nil
	}
	name := strings.TrimSuffix(secret.Name, suffix)
	observation := &strategies.KubernetesResourceObservation{
		Kind: strategies.ObservedKubeconfig, Namespace: secret.Namespace, Name: name,
		ID: uuids.ClusterID(name), KubeconfigAvailable: len(secret.Data["value"]) != 0,
	}
	for _, owner := range secret.OwnerReferences {
		if owner.Kind == strategies.ObservedCluster && owner.Name == name {
			observation.ClusterUID = string(owner.UID)
			break
		}
	}
	return observation
}

type kubeconfigState struct {
	available bool
	ownerUID  string
	initial   bool
}

type kubeconfigCluster struct {
	identity strategies.KubernetesResourceObservation
	initial  bool
}

// kubeconfigEvents joins independently ordered informer callbacks. It retains
// only identity and availability, never Secret contents or Cluster snapshots.
type kubeconfigEvents struct {
	mu       sync.Mutex
	clusters map[client.ObjectKey]kubeconfigCluster
	secrets  map[client.ObjectKey]kubeconfigState
	observed map[client.ObjectKey]*strategies.KubernetesResourceObservation
}

func (k *kubeconfigEvents) clusterChanged(change strategies.KubernetesResourceChange, notify func(strategies.KubernetesResourceChange)) {
	k.mu.Lock()
	defer k.mu.Unlock()
	notify(change)
	resource := change.Current
	if resource == nil {
		key := observationKey(change.Previous)
		delete(k.clusters, key)
		delete(k.observed, key)
		return
	}
	key := observationKey(resource)
	initial := change.Initial
	if previous, exists := k.clusters[key]; exists && previous.identity.UID != resource.UID {
		delete(k.observed, key)
	} else if exists {
		initial = previous.initial
	}
	k.clusters[key] = kubeconfigCluster{initial: initial, identity: strategies.KubernetesResourceObservation{
		Kind: strategies.ObservedKubeconfig, Namespace: resource.Namespace, Name: resource.Name,
		ID: resource.ID, UID: resource.UID,
	}}
	k.reconcile(key, change.Initial && k.secrets[key].initial, notify)
}

func (k *kubeconfigEvents) secretChanged(change strategies.KubernetesResourceChange, notify func(strategies.KubernetesResourceChange)) {
	k.mu.Lock()
	defer k.mu.Unlock()
	resource := change.Current
	if resource == nil {
		key := observationKey(change.Previous)
		delete(k.secrets, key)
		k.reconcile(key, change.Initial, notify)
		return
	}
	key := observationKey(resource)
	k.secrets[key] = kubeconfigState{available: resource.KubeconfigAvailable, ownerUID: resource.ClusterUID, initial: change.Initial}
	k.reconcile(key, change.Initial && k.clusters[key].initial, notify)
}

func (k *kubeconfigEvents) reconcile(key client.ObjectKey, initial bool, notify func(strategies.KubernetesResourceChange)) {
	cluster, exists := k.clusters[key]
	if !exists {
		return
	}
	secret := k.secrets[key]
	current := cluster.identity
	current.KubeconfigAvailable = secret.available && (secret.ownerUID == "" || secret.ownerUID == cluster.identity.UID)
	previous := k.observed[key]
	k.observed[key] = &current
	if !initial && ((previous == nil && current.KubeconfigAvailable) ||
		(previous != nil && previous.KubeconfigAvailable != current.KubeconfigAvailable)) {
		notify(strategies.KubernetesResourceChange{Previous: previous, Current: &current})
	}
}

func observationKey(resource *strategies.KubernetesResourceObservation) client.ObjectKey {
	return client.ObjectKey{Namespace: resource.Namespace, Name: resource.Name}
}
