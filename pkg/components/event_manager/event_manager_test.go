package eventmanager

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vatesfr/vmk-engine/pkg/strategies"

	"k8s.io/utils/ptr"
)

func TestHeartbeatSubscription(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		start := time.Now()
		events, err := NewEventManager().WatchKubernetesEvents(ctx)
		require.NoError(t, err)
		time.Sleep(4 * time.Second)
		synctest.Wait()
		select {
		case event := <-events:
			t.Fatalf("unexpected early event: %+v", event)
		default:
		}
		time.Sleep(time.Second)
		synctest.Wait()
		event := <-events
		assert.Equal(t, strategies.KubernetesEventPing, event.Type)
		assert.Equal(t, start.Add(5*time.Second), event.Timestamp)
		time.Sleep(5 * time.Second)
		synctest.Wait()
		event = <-events
		assert.Equal(t, start.Add(10*time.Second), event.Timestamp)
		cancel()
		synctest.Wait()
		_, open := <-events
		assert.False(t, open, "subscription closes after cancellation")
	})
}

func TestCancelSubscriptionWithSlowConsumer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		events, err := NewEventManager().WatchKubernetesEvents(ctx)
		require.NoError(t, err)
		// Leave the first delivery blocked, then cancel without reading it.
		time.Sleep(5 * time.Second)
		synctest.Wait()
		cancel()
		synctest.Wait()
		_, open := <-events
		assert.False(t, open, "a pending delivery must not prevent cleanup")
	})
}

func TestSubscriptionsAreIndependent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := NewEventManager()
		firstCtx, cancelFirst := context.WithCancel(context.Background())
		defer cancelFirst()
		secondCtx, cancelSecond := context.WithCancel(context.Background())
		defer cancelSecond()
		first, err := manager.WatchKubernetesEvents(firstCtx)
		require.NoError(t, err)
		second, err := manager.WatchKubernetesEvents(secondCtx)
		require.NoError(t, err)
		cancelFirst()
		synctest.Wait()
		_, open := <-first
		assert.False(t, open)
		time.Sleep(5 * time.Second)
		synctest.Wait()
		assert.Equal(t, strategies.KubernetesEventPing, (<-second).Type)
	})
}

func TestWatchKubernetesEventsRejectsInvalidSubscription(t *testing.T) {
	manager := NewEventManager()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events, err := manager.WatchKubernetesEvents(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, events)
}

const (
	testTagName               = "team"
	testTagCreatedEvent       = "cluster.tag.created"
	testTagUpdatedEvent       = "cluster.tag.updated"
	testTagDeletedEvent       = "cluster.tag.deleted"
	testScalingStartedEvent   = "cluster.scaling.started"
	testScalingCompletedEvent = "cluster.scaling.completed"
	testDeletingPhase         = "deleting"
)

func clusterObservation() *strategies.KubernetesResourceObservation {
	status := strategies.NodeTypeStatus{DesiredReplicas: 3, Replicas: 3, ReadyReplicas: 3, AvailableReplicas: 3}
	return &strategies.KubernetesResourceObservation{
		Kind: strategies.ObservedCluster, Name: "events-cluster", UID: "cluster-uid", Namespace: "management",
		Scaling: ptr.To(false),
		Cluster: &strategies.ClusterInfo{ControlPlanesStatus: status, WorkersStatus: status},
	}
}

func nodeObservation() *strategies.KubernetesResourceObservation {
	return &strategies.KubernetesResourceObservation{
		Kind: strategies.ObservedNode, UID: "machine-uid", Node: &strategies.ClusterNodeInfo{},
	}
}

func eventTypes(events []strategies.KubernetesEvent) []string {
	var result []string
	for _, event := range events {
		result = append(result, event.Type)
	}
	return result
}

func TestBusinessTransitions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before func() *strategies.KubernetesResourceObservation
		change func(*strategies.KubernetesResourceObservation)
		want   string
	}{
		{"available", clusterObservation, func(r *strategies.KubernetesResourceObservation) { r.Cluster.IsAvailable = true }, "cluster.available"},
		{
			"unavailable", func() *strategies.KubernetesResourceObservation {
				r := clusterObservation()
				r.Cluster.IsAvailable = true
				return r
			},
			func(r *strategies.KubernetesResourceObservation) { r.Cluster.IsAvailable = false }, "cluster.unavailable",
		},
		{"pause cluster", clusterObservation, func(r *strategies.KubernetesResourceObservation) { r.Cluster.Paused = true }, "cluster.reconciliation.paused"},
		{
			"resume cluster", func() *strategies.KubernetesResourceObservation {
				r := clusterObservation()
				r.Cluster.Paused = true
				return r
			},
			func(r *strategies.KubernetesResourceObservation) { r.Cluster.Paused = false }, "cluster.reconciliation.resumed",
		},
		{"delete cluster", clusterObservation, func(r *strategies.KubernetesResourceObservation) { r.Deleting = true }, eventClusterDeleting},
		{"description", clusterObservation, func(r *strategies.KubernetesResourceObservation) { r.Cluster.Description = "New description" }, "cluster.description.changed"},
		{"tag add", clusterObservation, func(r *strategies.KubernetesResourceObservation) { r.Cluster.Tags = map[string]string{testTagName: ""} }, testTagCreatedEvent},
		{
			"tag edit", func() *strategies.KubernetesResourceObservation {
				r := clusterObservation()
				r.Cluster.Tags = map[string]string{testTagName: ""}
				return r
			},
			func(r *strategies.KubernetesResourceObservation) {
				r.Cluster.Tags = map[string]string{testTagName: "platform"}
			}, testTagUpdatedEvent,
		},
		{
			"tag removal", func() *strategies.KubernetesResourceObservation {
				r := clusterObservation()
				r.Cluster.Tags = map[string]string{testTagName: ""}
				return r
			},
			func(r *strategies.KubernetesResourceObservation) { r.Cluster.Tags = nil }, testTagDeletedEvent,
		},
		{"node ready", nodeObservation, func(r *strategies.KubernetesResourceObservation) { r.Node.Status = nodeReadyStatus }, "node.ready"},
		{
			"node unknown", func() *strategies.KubernetesResourceObservation {
				r := nodeObservation()
				r.Node.Status = nodeReadyStatus
				return r
			},
			func(r *strategies.KubernetesResourceObservation) { r.Node.Status = "Unknown" }, "node.not_ready",
		},
		{"pause node", nodeObservation, func(r *strategies.KubernetesResourceObservation) { r.Node.Paused = true }, "node.reconciliation.paused"},
		{
			"resume node", func() *strategies.KubernetesResourceObservation {
				r := nodeObservation()
				r.Node.Paused = true
				return r
			},
			func(r *strategies.KubernetesResourceObservation) { r.Node.Paused = false }, "node.reconciliation.resumed",
		},
		{"delete node", nodeObservation, func(r *strategies.KubernetesResourceObservation) { r.Deleting = true }, "node.deleting"},
		{"scaling started", clusterObservation, func(r *strategies.KubernetesResourceObservation) { r.Scaling = ptr.To(true) }, testScalingStartedEvent},
		{
			"scaling progress", func() *strategies.KubernetesResourceObservation {
				r := clusterObservation()
				r.Scaling = ptr.To(true)
				return r
			},
			func(r *strategies.KubernetesResourceObservation) { r.Cluster.WorkersStatus.ReadyReplicas = 2 }, "cluster.scaling.progress",
		},
		{
			"scaling completed", func() *strategies.KubernetesResourceObservation {
				r := clusterObservation()
				r.Scaling = ptr.To(true)
				return r
			},
			func(r *strategies.KubernetesResourceObservation) { r.Scaling = ptr.To(false) }, testScalingCompletedEvent,
		},
		{"upgrade started", clusterObservation, func(r *strategies.KubernetesResourceObservation) {
			r.Upgrade = &strategies.UpgradeOperation{ID: "upgrade-1", Locked: true}
		}, "cluster.upgrade.started"},
		{"upgrade progress", activeUpgradeObservation, func(r *strategies.KubernetesResourceObservation) { r.Cluster.WorkersStatus.UpToDateReplicas = 1 }, "cluster.upgrade.progress"},
		{"upgrade completed", activeUpgradeObservation, func(r *strategies.KubernetesResourceObservation) {
			op := *r.Upgrade
			op.Locked = false
			op.CompletedAt = "2026-10-22T10:00:00Z"
			r.Upgrade = &op
		}, "cluster.upgrade.completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.before()
			after := *before
			if before.Cluster != nil {
				copy := *before.Cluster
				after.Cluster = &copy
			}
			if before.Node != nil {
				copy := *before.Node
				after.Node = &copy
			}
			tc.change(&after)
			events := NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: before, Current: &after})
			require.Equal(t, []string{tc.want}, eventTypes(events))
			if events[0].Tag != nil {
				if tc.want == testTagCreatedEvent {
					assert.Nil(t, events[0].Tag.Previous)
					require.NotNil(t, events[0].Tag.Current)
					assert.Empty(t, *events[0].Tag.Current)
				}
				if tc.want == testTagDeletedEvent {
					require.NotNil(t, events[0].Tag.Previous)
					assert.Empty(t, *events[0].Tag.Previous)
					assert.Nil(t, events[0].Tag.Current)
				}
			}
		})
	}
}

func activeUpgradeObservation() *strategies.KubernetesResourceObservation {
	r := clusterObservation()
	r.Upgrade = &strategies.UpgradeOperation{ID: "upgrade-1", Version: "v1.36.5", Locked: true}
	return r
}

func TestResourceLifecycleAndInitialList(t *testing.T) {
	for _, resource := range []*strategies.KubernetesResourceObservation{clusterObservation(), nodeObservation()} {
		prefix := "cluster"
		if resource.Node != nil {
			prefix = "node"
		}
		assert.Empty(t, NewEventManager().transitions(strategies.KubernetesResourceChange{Current: resource, Initial: true}))
		assert.Empty(t, NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: resource, Current: resource}))
		assert.Equal(t, []string{prefix + ".created"}, eventTypes(NewEventManager().transitions(strategies.KubernetesResourceChange{Current: resource})))
		assert.Equal(t, []string{prefix + ".deleted"}, eventTypes(NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: resource})))
		replacement := *resource
		replacement.UID = "replacement-uid"
		assert.Equal(t, []string{prefix + ".deleted", prefix + ".created"}, eventTypes(NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: resource, Current: &replacement})))
	}
}

func TestKubeconfigAvailabilityTransitions(t *testing.T) {
	resource := clusterObservation()
	resource.Kind = strategies.ObservedKubeconfig
	assert.Empty(t, NewEventManager().transitions(strategies.KubernetesResourceChange{Current: resource}))
	available := *resource
	available.KubeconfigAvailable = true
	assert.Equal(t, []string{"cluster.kubeconfig.available"}, eventTypes(NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: resource, Current: &available})))
	assert.Equal(t, []string{"cluster.kubeconfig.unavailable"}, eventTypes(NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: &available, Current: resource})))
	assert.Equal(t, []string{"cluster.kubeconfig.unavailable"}, eventTypes(NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: &available})))
	assert.Empty(t, NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: &available, Current: &available}))
}

func TestDoNotInventOperationCompletion(t *testing.T) {
	before := clusterObservation()
	before.Scaling = ptr.To(true)
	after := clusterObservation()
	after.Scaling = nil
	assert.Empty(t, NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: before, Current: after}))
	after.Scaling = ptr.To(false)
	after.Cluster.WorkersStatus.ReadyReplicas = 1
	assert.Empty(t, NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: before, Current: after}))
	before = activeUpgradeObservation()
	after = clusterObservation()
	assert.Empty(t, NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: before, Current: after}), "a removed lock is not proof of completion")
	after = activeUpgradeObservation()
	op := *after.Upgrade
	op.Locked = false
	op.CompletedAt = "complete"
	after.Upgrade = &op
	after.Deleting = true
	assert.Equal(t, []string{eventClusterDeleting}, eventTypes(NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: before, Current: after})))
}

func TestRetainScalingUntilReplicaConvergence(t *testing.T) {
	manager := NewEventManager()
	before := clusterObservation()
	before.Scaling = ptr.To(true)
	after := clusterObservation()
	after.Cluster.WorkersStatus.ReadyReplicas = 1
	manager.handleChange(strategies.KubernetesResourceChange{Previous: before, Current: after})
	assert.True(t, manager.scaling[before.UID])
	manager.handleChange(strategies.KubernetesResourceChange{Previous: after, Current: clusterObservation()})
	assert.Empty(t, manager.scaling)
}

func TestScalingEventsAcrossUnknownConditionsAndLaggingCounters(t *testing.T) {
	manager := NewEventManager()
	before := clusterObservation()
	active := clusterObservation()
	active.Scaling = ptr.To(true)
	assert.Equal(t, []string{testScalingStartedEvent}, eventTypes(manager.transitions(strategies.KubernetesResourceChange{Previous: before, Current: active})))
	unknown := clusterObservation()
	unknown.Scaling = nil
	unknown.Cluster.WorkersStatus.ReadyReplicas = 2
	assert.Empty(t, manager.transitions(strategies.KubernetesResourceChange{Previous: active, Current: unknown}))
	lagging := clusterObservation()
	lagging.Cluster.WorkersStatus.ReadyReplicas = 2
	assert.Empty(t, manager.transitions(strategies.KubernetesResourceChange{Previous: unknown, Current: lagging}))
	complete := clusterObservation()
	assert.Equal(t, []string{testScalingCompletedEvent}, eventTypes(manager.transitions(strategies.KubernetesResourceChange{Previous: lagging, Current: complete})))
	assert.Empty(t, manager.scaling)
	assert.Empty(t, manager.transitions(strategies.KubernetesResourceChange{Previous: complete, Current: complete}), "completion is emitted once")
}

func TestScalingTrackingClearedByClusterLifecycle(t *testing.T) {
	for _, ending := range []string{testDeletingPhase, "deleted", "replaced"} {
		t.Run(ending, func(t *testing.T) {
			manager := NewEventManager()
			before := clusterObservation()
			active := clusterObservation()
			active.Scaling = ptr.To(true)
			manager.transitions(strategies.KubernetesResourceChange{Previous: before, Current: active})
			require.True(t, manager.scaling[active.UID])
			after := clusterObservation()
			switch ending {
			case testDeletingPhase:
				after.Deleting = true
				after.Scaling = ptr.To(true)
			case "deleted":
				after = nil
			default:
				after.UID = "replacement-uid"
			}
			events := manager.transitions(strategies.KubernetesResourceChange{Previous: active, Current: after})
			assert.NotContains(t, eventTypes(events), testScalingCompletedEvent, "interruption is not success")
			assert.Empty(t, manager.scaling)
			if ending == testDeletingPhase {
				assert.Empty(t, manager.transitions(strategies.KubernetesResourceChange{Previous: after, Current: after}))
				assert.Empty(t, manager.scaling, "later deletion updates must not reintroduce scaling tracking")
			}
		})
	}
}

func TestMultipleTagEventsKeepTheirOwnContext(t *testing.T) {
	before, after := clusterObservation(), clusterObservation()
	before.Cluster.Tags = map[string]string{"a": "old", "b": "removed"}
	after.Cluster.Tags = map[string]string{"a": "new", "c": ""}
	events := NewEventManager().transitions(strategies.KubernetesResourceChange{Previous: before, Current: after})
	require.Equal(t, []string{testTagUpdatedEvent, testTagDeletedEvent, testTagCreatedEvent}, eventTypes(events))
	want := []strategies.KubernetesTagChange{
		{Name: "a", Previous: ptr.To("old"), Current: ptr.To("new")},
		{Name: "b", Previous: ptr.To("removed")},
		{Name: "c", Current: ptr.To("")},
	}
	for i, event := range events {
		require.NotNil(t, event.Tag)
		assert.Equal(t, want[i], *event.Tag)
		assert.Same(t, before, event.Previous)
		assert.Same(t, after, event.Resource)
		assert.False(t, event.Timestamp.IsZero())
	}
}

func TestBusinessEventFanoutAndSlowSubscriber(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := NewEventManager()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		first, err := manager.WatchKubernetesEvents(ctx)
		require.NoError(t, err)
		second, err := manager.WatchKubernetesEvents(ctx)
		require.NoError(t, err)
		manager.handleChange(strategies.KubernetesResourceChange{Current: clusterObservation()})
		synctest.Wait()
		assert.Equal(t, "cluster.created", (<-first).Type)
		assert.Equal(t, "cluster.created", (<-second).Type)
		synctest.Wait()
		for i := 0; i < subscriberQueueSize+2; i++ {
			manager.handleChange(strategies.KubernetesResourceChange{Current: nodeObservation()})
		}
		synctest.Wait()
		_, open := <-first
		assert.False(t, open, "overflow closes the subscription rather than losing events silently")
		_, open = <-second
		assert.False(t, open)
		assert.Empty(t, manager.subscribers)
	})
}

func TestSlowSubscriberDoesNotInterruptHealthySubscriber(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager := NewEventManager()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		slow, err := manager.WatchKubernetesEvents(ctx)
		require.NoError(t, err)
		healthy, err := manager.WatchKubernetesEvents(ctx)
		require.NoError(t, err)
		for i := 0; i < subscriberQueueSize+3; i++ {
			manager.handleChange(strategies.KubernetesResourceChange{Current: nodeObservation()})
			assert.Equal(t, "node.created", (<-healthy).Type)
			synctest.Wait()
		}
		_, open := <-slow
		assert.False(t, open)
		assert.Len(t, manager.subscribers, 1)
	})
}
