package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-fuego/fuego"

	"capv-proxy/pkg/strategies"
)

type KubernetesEventsHandler struct {
	eventService strategies.ForWatchingKubernetesEvents
}

type HeartbeatPayload struct {
	Ping int64 `json:"ping"`
}

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
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	controller := http.NewResponseController(w)
	// Send the headers immediately, without waiting for the first heartbeat.
	if err := controller.Flush(); err != nil {
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
			data, err := json.Marshal(HeartbeatPayload{Ping: event.Timestamp.UnixMilli()})
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
		}
	}
}
