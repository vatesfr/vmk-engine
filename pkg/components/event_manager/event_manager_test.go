package eventmanager

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"capv-proxy/pkg/strategies"
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
