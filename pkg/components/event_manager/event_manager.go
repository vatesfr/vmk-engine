package eventmanager

import (
	"context"
	"time"

	"capv-proxy/pkg/strategies"
)

const heartbeatInterval = 5 * time.Second

// EventManager owns Kubernetes event subscriptions independently of HTTP/SSE.
type EventManager struct{}

var _ strategies.ForWatchingKubernetesEvents = EventManager{}

func NewEventManager() EventManager {
	return EventManager{}
}

func (m EventManager) WatchKubernetesEvents(ctx context.Context) (<-chan strategies.KubernetesEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	events := make(chan strategies.KubernetesEvent)
	ticker := time.NewTicker(heartbeatInterval)
	go func() {
		defer close(events)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				// A slow consumer cannot accumulate an unbounded queue and
				// cancellation also interrupts a pending delivery.
				select {
				case <-ctx.Done():
					return
				case events <- strategies.KubernetesEvent{Type: strategies.KubernetesEventPing, Timestamp: now}:
				}
			}
		}
	}()
	return events, nil
}
