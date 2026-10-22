package eventmanager

import (
	"context"
	"reflect"
	"sort"
	"sync"
	"time"

	"capv-proxy/pkg/strategies"
)

const (
	heartbeatInterval    = 5 * time.Second
	subscriberQueueSize  = 128
	eventClusterDeleting = "cluster.deleting"
	nodeReadyStatus      = "Ready"
)

// EventManager translates resource observations into business events and fans
// them out to independent subscribers without blocking Kubernetes informers.
type EventManager struct {
	mu          sync.Mutex
	subscribers map[*subscriber]struct{}
	scaling     map[string]bool
}

type subscriber struct {
	queue chan strategies.KubernetesEvent
	done  chan struct{}
}

var _ strategies.ForWatchingKubernetesEvents = (*EventManager)(nil)

func NewEventManager() *EventManager {
	return &EventManager{subscribers: make(map[*subscriber]struct{}), scaling: make(map[string]bool)}
}

// Start connects one shared resource observer. The configurator owns its lifetime.
func (m *EventManager) Start(ctx context.Context, source strategies.ForObservingKubernetesResources) (func(), error) {
	return source.ObserveKubernetesResources(ctx, m.handleChange)
}

func (m *EventManager) WatchKubernetesEvents(ctx context.Context) (<-chan strategies.KubernetesEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sub := &subscriber{queue: make(chan strategies.KubernetesEvent, subscriberQueueSize), done: make(chan struct{})}
	m.mu.Lock()
	m.subscribers[sub] = struct{}{}
	m.mu.Unlock()
	events := make(chan strategies.KubernetesEvent)
	ticker := time.NewTicker(heartbeatInterval)
	go func() {
		defer close(events)
		defer ticker.Stop()
		defer m.unsubscribe(sub)
		for {
			var event strategies.KubernetesEvent
			select {
			case <-ctx.Done():
				return
			case <-sub.done:
				return
			case now := <-ticker.C:
				event = strategies.KubernetesEvent{Type: strategies.KubernetesEventPing, Timestamp: now}
			case event = <-sub.queue:
			}
			select {
			case <-ctx.Done():
				return
			case <-sub.done:
				return
			case events <- event:
			}
		}
	}()
	return events, nil
}

func (m *EventManager) unsubscribe(sub *subscriber) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.subscribers[sub]; exists {
		delete(m.subscribers, sub)
		close(sub.done)
	}
}

func (m *EventManager) handleChange(change strategies.KubernetesResourceChange) {
	m.mu.Lock()
	defer m.mu.Unlock()
	events := m.transitions(change)
	if len(events) == 0 {
		return
	}
	// Publish a transition batch together even when different informers call us
	// concurrently. Only this manager writes to the bounded business queues.
	for sub := range m.subscribers {
		if !enqueueEvents(sub, events) {
			// A gap requires a fresh REST read after reconnection; silently
			// continuing would leave the frontend with an incorrect state.
			delete(m.subscribers, sub)
			close(sub.done)
		}
	}
}

func enqueueEvents(sub *subscriber, events []strategies.KubernetesEvent) bool {
	for _, event := range events {
		select {
		case sub.queue <- event:
		default:
			return false
		}
	}
	return true
}

// transitions routes observations; each resource handler owns its lifecycle
// and operation tracking. The caller holds m.mu.
func (m *EventManager) transitions(change strategies.KubernetesResourceChange) []strategies.KubernetesEvent {
	if change.Initial {
		return nil
	}
	before, after := change.Previous, change.Current
	if before != nil && after != nil && before.UID != after.UID {
		return append(m.transitions(strategies.KubernetesResourceChange{Previous: before}), m.transitions(strategies.KubernetesResourceChange{Current: after})...)
	}
	resource := after
	if resource == nil {
		resource = before
	}
	if resource == nil {
		return nil
	}
	switch resource.Kind {
	case strategies.ObservedCluster:
		return m.clusterTransitions(before, after)
	case strategies.ObservedNode:
		return nodeTransitions(before, after)
	case strategies.ObservedKubeconfig:
		return kubeconfigTransitions(before, after)
	default:
		return nil
	}
}

func newEvent(name string, before, after *strategies.KubernetesResourceObservation) strategies.KubernetesEvent {
	resource := after
	if resource == nil {
		resource = before
	}
	return strategies.KubernetesEvent{Type: name, Timestamp: time.Now().UTC(), Resource: resource, Previous: before}
}

func kubeconfigTransitions(before, after *strategies.KubernetesResourceObservation) []strategies.KubernetesEvent {
	oldAvailable := before != nil && before.KubeconfigAvailable
	newAvailable := after != nil && after.KubeconfigAvailable
	if oldAvailable == newAvailable {
		return nil
	}
	if newAvailable {
		return []strategies.KubernetesEvent{newEvent("cluster.kubeconfig.available", before, after)}
	}
	return []strategies.KubernetesEvent{newEvent("cluster.kubeconfig.unavailable", before, after)}
}

func nodeTransitions(before, after *strategies.KubernetesResourceObservation) []strategies.KubernetesEvent {
	if before == nil {
		return []strategies.KubernetesEvent{newEvent("node.created", before, after)}
	}
	if after == nil {
		return []strategies.KubernetesEvent{newEvent("node.deleted", before, after)}
	}
	if before.Node == nil || after.Node == nil {
		return nil
	}
	if before.Node.ID != after.Node.ID {
		return append(nodeTransitions(before, nil), nodeTransitions(nil, after)...)
	}
	var events []strategies.KubernetesEvent
	if before.Node.Status != nodeReadyStatus && after.Node.Status == nodeReadyStatus {
		events = append(events, newEvent("node.ready", before, after))
	} else if before.Node.Status == nodeReadyStatus && after.Node.Status != nodeReadyStatus {
		events = append(events, newEvent("node.not_ready", before, after))
	}
	events = append(events, pauseTransitions("node", before, after, before.Node.Paused, after.Node.Paused)...)
	if !before.Deleting && after.Deleting {
		events = append(events, newEvent("node.deleting", before, after))
	}
	return events
}

func pauseTransitions(prefix string, before, after *strategies.KubernetesResourceObservation, wasPaused, paused bool) []strategies.KubernetesEvent {
	if wasPaused == paused {
		return nil
	}
	if paused {
		return []strategies.KubernetesEvent{newEvent(prefix+".reconciliation.paused", before, after)}
	}
	return []strategies.KubernetesEvent{newEvent(prefix+".reconciliation.resumed", before, after)}
}

func (m *EventManager) clusterTransitions(before, after *strategies.KubernetesResourceObservation) []strategies.KubernetesEvent {
	if before == nil {
		return []strategies.KubernetesEvent{newEvent("cluster.created", before, after)}
	}
	if after == nil {
		delete(m.scaling, before.UID)
		return []strategies.KubernetesEvent{newEvent("cluster.deleted", before, after)}
	}
	if before.Cluster == nil || after.Cluster == nil {
		return nil
	}
	oldInfo, info := before.Cluster, after.Cluster
	var events []strategies.KubernetesEvent
	if oldInfo.IsAvailable != info.IsAvailable {
		if info.IsAvailable {
			events = append(events, newEvent("cluster.available", before, after))
		} else {
			events = append(events, newEvent("cluster.unavailable", before, after))
		}
	}
	events = append(events, pauseTransitions("cluster", before, after, oldInfo.Paused, info.Paused)...)
	if !before.Deleting && after.Deleting {
		events = append(events, newEvent(eventClusterDeleting, before, after))
	}
	if oldInfo.Description != info.Description {
		events = append(events, newEvent("cluster.description.changed", before, after))
	}
	events = append(events, tagTransitions(before, after)...)
	events = append(events, m.scalingTransitions(before, after)...)
	if !after.Deleting {
		events = append(events, upgradeTransitions(before, after)...)
	}
	return events
}

func tagTransitions(before, after *strategies.KubernetesResourceObservation) []strategies.KubernetesEvent {
	oldTags, tags := before.Cluster.Tags, after.Cluster.Tags
	keys := make(map[string]struct{}, len(oldTags)+len(tags))
	for key := range oldTags {
		keys[key] = struct{}{}
	}
	for key := range tags {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	var events []strategies.KubernetesEvent
	for _, key := range ordered {
		previous, existed := oldTags[key]
		current, exists := tags[key]
		if existed && exists && previous == current {
			continue
		}
		change := &strategies.KubernetesTagChange{Name: key}
		if existed {
			change.Previous = &previous
		}
		if exists {
			change.Current = &current
		}
		name := "cluster.tag.updated"
		if !existed {
			name = "cluster.tag.created"
		} else if !exists {
			name = "cluster.tag.deleted"
		}
		event := newEvent(name, before, after)
		event.Tag = change
		events = append(events, event)
	}
	return events
}

func statusesChanged(before, after *strategies.KubernetesResourceObservation) bool {
	return !reflect.DeepEqual(before.Cluster.ControlPlanesStatus, after.Cluster.ControlPlanesStatus) ||
		!reflect.DeepEqual(before.Cluster.WorkersStatus, after.Cluster.WorkersStatus)
}

func converged(status strategies.NodeTypeStatus) bool {
	return status.DesiredReplicas > 0 && status.Replicas == status.DesiredReplicas &&
		status.ReadyReplicas == status.DesiredReplicas && status.AvailableReplicas == status.DesiredReplicas
}

func (m *EventManager) scalingTransitions(before, after *strategies.KubernetesResourceObservation) []strategies.KubernetesEvent {
	if after.Deleting {
		delete(m.scaling, after.UID)
		return nil
	}
	// Keep tracking an observed scaling across Unknown conditions and lagging
	// counters. Tracking changes here, independently of the emitted event names.
	wasScaling := m.scaling[before.UID] || (before.Scaling != nil && *before.Scaling)
	if wasScaling {
		m.scaling[before.UID] = true
	}
	if after.Scaling == nil {
		return nil
	}
	if *after.Scaling {
		m.scaling[after.UID] = true
		if !wasScaling {
			return []strategies.KubernetesEvent{newEvent("cluster.scaling.started", before, after)}
		}
		if statusesChanged(before, after) {
			return []strategies.KubernetesEvent{newEvent("cluster.scaling.progress", before, after)}
		}
		return nil
	}
	if wasScaling && converged(after.Cluster.ControlPlanesStatus) && converged(after.Cluster.WorkersStatus) {
		delete(m.scaling, after.UID)
		return []strategies.KubernetesEvent{newEvent("cluster.scaling.completed", before, after)}
	}
	return nil
}

func upgradeTransitions(before, after *strategies.KubernetesResourceObservation) []strategies.KubernetesEvent {
	previous, current := before.Upgrade, after.Upgrade
	if current == nil {
		return nil
	}
	var name string
	if current.Locked && (previous == nil || previous.ID != current.ID || !previous.Locked) {
		name = "cluster.upgrade.started"
	} else if previous != nil && previous.ID == current.ID && previous.Locked && !current.Locked && current.CompletedAt != "" {
		name = "cluster.upgrade.completed"
	} else if current.Locked && previous != nil && previous.ID == current.ID && statusesChanged(before, after) {
		name = "cluster.upgrade.progress"
	} else {
		return nil
	}
	event := newEvent(name, before, after)
	event.Upgrade = current
	return []strategies.KubernetesEvent{event}
}
