package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-fuego/fuego"
	"github.com/gofrs/uuid"

	"github.com/vatesfr/vmk-engine/pkg/strategies"
)

type KubernetesEventsHandler struct {
	eventService strategies.ForWatchingKubernetesEvents
}

type HeartbeatPayload struct {
	Ping int64 `json:"ping"`
}

type KubernetesEventPayload struct {
	ObservedAt  string           `json:"observed_at"`
	Namespace   string           `json:"namespace"`
	ID          uuid.UUID        `json:"id"`
	Name        string           `json:"name"`
	UID         string           `json:"uid"`
	ClusterID   *uuid.UUID       `json:"cluster_id,omitempty"`
	ClusterName string           `json:"cluster_name,omitempty"`
	Cluster     *ClusterInfo     `json:"cluster,omitempty"`
	Node        *ClusterNodeInfo `json:"node,omitempty"`
	Context     EventContext     `json:"context,omitempty"`
}

// EventContext restricts event-specific data to the supported JSON shapes.
type EventContext interface {
	isEventContext()
}

type TagChangePayload struct {
	Tag           string  `json:"tag"`
	PreviousValue *string `json:"previous_value"`
	Value         *string `json:"value"`
}

func (TagChangePayload) isEventContext() {}

type DescriptionChangedContext struct {
	PreviousDescription string `json:"previous_description"`
	Description         string `json:"description"`
}

func (DescriptionChangedContext) isEventContext() {}

type KubeconfigContext struct {
	Available bool `json:"available"`
}

func (KubeconfigContext) isEventContext() {}

type ScalingContext struct {
	Group              string      `json:"group"`
	ControlPlaneStatus *NodeStatus `json:"control_plane_status"`
	WorkerStatus       *NodeStatus `json:"worker_status"`
}

const (
	clusterEventGroup = "cluster"
	eventWriteTimeout = 10 * time.Second
)

func (ScalingContext) isEventContext() {}

type UpgradeContext struct {
	ScalingContext
	OperationID   string `json:"operation_id"`
	TargetVersion string `json:"target_version"`
}

func (UpgradeContext) isEventContext() {}

func NewKubernetesEventsHandler(eventService strategies.ForWatchingKubernetesEvents) KubernetesEventsHandler {
	return KubernetesEventsHandler{eventService: eventService}
}

// GetKubernetesEvents encodes the event subscription as SSE and releases it when the
// client disconnects or a write fails.
func (h KubernetesEventsHandler) GetKubernetesEvents(w http.ResponseWriter, r *http.Request) {
	// net/http also matches HEAD requests to GET routes. A HEAD must not start
	// an indefinitely running heartbeat loop.
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	events, err := h.eventService.WatchKubernetesEvents(ctx)
	if err != nil {
		fuego.SendJSONError(w, r, toHTTPError(err))
		return
	}
	streamEvents(w, r, events)
}

func streamEvents(w http.ResponseWriter, r *http.Request, events <-chan strategies.KubernetesEvent) {
	controller := http.NewResponseController(w)
	// Bound every network write, including the initial header flush. Clear the
	// deadline between frames so idle time does not consume the next write's budget.
	setDeadline := func(deadline time.Time) error {
		err := controller.SetWriteDeadline(deadline)
		if errors.Is(err, http.ErrNotSupported) {
			// In-memory writers used by embedding applications may have no socket.
			return nil
		}
		return err
	}
	if err := setDeadline(time.Now().Add(eventWriteTimeout)); err != nil {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Send the headers immediately, without waiting for the first heartbeat.
	if err := controller.Flush(); err != nil {
		return
	}
	if err := setDeadline(time.Time{}); err != nil {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(eventPayload(event))
			if err != nil {
				return
			}
			if err := setDeadline(time.Now().Add(eventWriteTimeout)); err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
			if err := setDeadline(time.Time{}); err != nil {
				return
			}
		}
	}
}

func eventPayload(event strategies.KubernetesEvent) any {
	if event.Type == strategies.KubernetesEventPing {
		return HeartbeatPayload{Ping: event.Timestamp.UnixMilli()}
	}
	resource := event.Resource
	payload := KubernetesEventPayload{
		ID:         resource.ID,
		ObservedAt: event.Timestamp.UTC().Format(time.RFC3339Nano),
		Namespace:  resource.Namespace, Name: resource.Name, UID: resource.UID,
		ClusterName: resource.ClusterName,
	}
	kubeconfig := strings.HasPrefix(event.Type, "cluster.kubeconfig.")
	deleted := event.Type == "cluster.deleted" || event.Type == "node.deleted"
	if resource.Cluster != nil {
		payload.ID = resource.Cluster.ID
		if !deleted && !kubeconfig {
			info := toClusterInfo(*resource.Cluster)
			payload.Cluster = &info
		}
	}
	if resource.Node != nil {
		payload.ID = resource.Node.ID
		payload.ClusterID = &resource.Node.ParentCluster
		if !deleted {
			info := toNodeInfo(*resource.Node)
			payload.Node = &info
		}
	}
	switch {
	case event.Tag != nil:
		payload.Context = TagChangePayload{Tag: event.Tag.Name, PreviousValue: event.Tag.Previous, Value: event.Tag.Current}
	case event.Type == "cluster.description.changed":
		payload.Context = DescriptionChangedContext{
			PreviousDescription: event.Previous.Cluster.Description,
			Description:         resource.Cluster.Description,
		}
	case strings.HasPrefix(event.Type, "cluster.kubeconfig."):
		payload.Context = KubeconfigContext{Available: event.Type == "cluster.kubeconfig.available"}
	case strings.HasPrefix(event.Type, "cluster.scaling.") || strings.HasPrefix(event.Type, "cluster.upgrade."):
		context := ScalingContext{
			Group:              clusterEventGroup,
			ControlPlaneStatus: NewFromNodeStatus(resource.Cluster.ControlPlanesStatus),
			WorkerStatus:       NewFromNodeStatus(resource.Cluster.WorkersStatus),
		}
		payload.Context = context
		if event.Upgrade != nil {
			payload.Context = UpgradeContext{
				ScalingContext: context,
				OperationID:    event.Upgrade.ID,
				TargetVersion:  event.Upgrade.Version,
			}
		}
	}
	return payload
}
