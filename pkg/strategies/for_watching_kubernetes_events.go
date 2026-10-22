package strategies

import (
	"context"
	"time"
)

//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=for_watching_kubernetes_events.go -destination=mocks/for_watching_kubernetes_events.go -package=mocks
type ForWatchingKubernetesEvents interface {
	// WatchKubernetesEvents opens a subscription for the service's Kubernetes scope.
	// Canceling ctx releases the subscription and closes the returned channel.
	WatchKubernetesEvents(ctx context.Context) (<-chan KubernetesEvent, error)
}

const KubernetesEventPing = "ping"

// KubernetesEvent is a transport-independent observation. The heartbeat is the
// only event currently produced; its timestamp is converted by the HTTP DTO.
type KubernetesEvent struct {
	Type      string
	Timestamp time.Time
}
