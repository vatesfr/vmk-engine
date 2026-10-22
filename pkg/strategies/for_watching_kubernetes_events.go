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

// KubernetesEvent contains an immutable resource snapshot and its transition.
// The HTTP component owns the JSON representation.
type KubernetesEvent struct {
	Type      string
	Timestamp time.Time
	Resource  *KubernetesResourceObservation
	Previous  *KubernetesResourceObservation
	Tag       *KubernetesTagChange
	Upgrade   *UpgradeOperation
}

type KubernetesTagChange struct {
	Name     string
	Previous *string
	Current  *string
}
