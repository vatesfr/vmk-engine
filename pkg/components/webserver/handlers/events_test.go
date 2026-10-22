package handlers

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventmanager "capv-proxy/pkg/components/event_manager"
	"capv-proxy/pkg/strategies"
)

type failedStreamWriter struct {
	*httptest.ResponseRecorder
	failFlush bool
	writes    int
}

type deadlineStreamWriter struct {
	*httptest.ResponseRecorder
	connection net.Conn
	blockFlush bool
	entered    chan struct{}
	once       sync.Once
	writeError error
}

func (w *deadlineStreamWriter) SetWriteDeadline(deadline time.Time) error {
	return w.connection.SetWriteDeadline(deadline)
}

func (w *deadlineStreamWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	n, err := w.connection.Write(data)
	w.writeError = err
	return n, err
}

func (w *deadlineStreamWriter) FlushError() error {
	if w.blockFlush {
		_, err := w.Write([]byte("headers"))
		return err
	}
	w.Flush()
	return nil
}

func TestStreamWriteDeadlineReleasesBlockedConnection(t *testing.T) {
	for _, blockFlush := range []bool{false, true} {
		name := "business frame"
		if blockFlush {
			name = "initial headers"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server, client := net.Pipe()
				defer server.Close()
				defer client.Close()
				writer := &deadlineStreamWriter{
					ResponseRecorder: httptest.NewRecorder(), connection: server,
					blockFlush: blockFlush, entered: make(chan struct{}),
				}
				events := make(chan strategies.KubernetesEvent, 1)
				events <- strategies.KubernetesEvent{Type: strategies.KubernetesEventPing, Timestamp: time.Now()}
				done := make(chan struct{})
				go func() {
					streamEvents(writer, httptest.NewRequest(http.MethodGet, "/", nil), events)
					close(done)
				}()
				<-writer.entered
				close(events) // An overflow closes the subscription while HTTP is blocked.
				synctest.Wait()
				select {
				case <-done:
					t.Fatal("write should still be waiting for its deadline")
				default:
				}
				time.Sleep(eventWriteTimeout)
				synctest.Wait()
				select {
				case <-done:
				default:
					t.Fatal("stream did not return after its write deadline")
				}
				assert.ErrorIs(t, writer.writeError, os.ErrDeadlineExceeded)
			})
		})
	}
}

func TestKubeconfigPayloadContainsOnlyIdentityAndAvailability(t *testing.T) {
	id := uuid.Must(uuid.NewV4())
	for _, available := range []bool{false, true} {
		eventType := "cluster.kubeconfig.unavailable"
		if available {
			eventType = "cluster.kubeconfig.available"
		}
		resource := &strategies.KubernetesResourceObservation{
			Kind: strategies.ObservedKubeconfig, ID: id, UID: "cluster-uid", Namespace: "management", Name: "test-cluster",
			KubeconfigAvailable: available,
		}
		encoded, err := json.Marshal(eventPayload(strategies.KubernetesEvent{Type: eventType, Resource: resource}))
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(encoded, &payload))
		assert.Equal(t, id.String(), payload["id"])
		assert.Equal(t, "cluster-uid", payload["uid"])
		assert.NotContains(t, payload, "cluster")
		assert.NotContains(t, payload, "node")
		assert.Equal(t, map[string]any{"available": available}, payload["context"])
	}
}

func (w *failedStreamWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("client disconnected")
}

func (w *failedStreamWriter) FlushError() error {
	if w.failFlush {
		return errors.New("flush failed")
	}
	w.Flush()
	return nil
}

func TestKubernetesEventsReleasesSubscriptionOnIOFailure(t *testing.T) {
	for _, failFlush := range []bool{false, true} {
		name := "write failure"
		if failFlush {
			name = "flush failure"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				handler := NewKubernetesEventsHandler(eventmanager.NewEventManager())
				writer := &failedStreamWriter{ResponseRecorder: httptest.NewRecorder(), failFlush: failFlush}
				request := httptest.NewRequest(http.MethodGet, "/", nil)
				done := make(chan struct{})
				go func() {
					handler.GetKubernetesEvents(writer, request)
					close(done)
				}()
				time.Sleep(5 * time.Second)
				synctest.Wait()
				select {
				case <-done:
				default:
					t.Fatal("stream did not stop after I/O failure")
				}
				if failFlush {
					assert.Zero(t, writer.writes)
				} else {
					assert.Equal(t, 1, writer.writes)
				}
			})
		})
	}
}

func TestStreamEventsReturnsWhenSubscriptionCloses(t *testing.T) {
	events := make(chan strategies.KubernetesEvent)
	close(events)
	response := httptest.NewRecorder()
	streamEvents(response, httptest.NewRequest(http.MethodGet, "/", nil), events)
	assert.Empty(t, response.Body.String())
}

func TestBusinessPayloadIdentityAndTagValues(t *testing.T) {
	id := uuid.Must(uuid.FromString("bca86d96-6ce6-41c3-8172-ff62b315122c"))
	empty := ""
	event := strategies.KubernetesEvent{
		Type: "cluster.tag.created", Timestamp: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC),
		Resource: &strategies.KubernetesResourceObservation{Namespace: "management", Name: "payload-cluster", UID: "real-kubernetes-uid", Cluster: &strategies.ClusterInfo{ID: id}},
		Tag:      &strategies.KubernetesTagChange{Name: "environment", Current: &empty},
	}
	encoded, err := json.Marshal(eventPayload(event))
	require.NoError(t, err)
	var data map[string]any
	require.NoError(t, json.Unmarshal(encoded, &data))
	assert.Equal(t, id.String(), data["id"])
	assert.Equal(t, "real-kubernetes-uid", data["uid"])
	assert.Equal(t, "2026-10-22T10:00:00Z", data["observed_at"])
	assert.Equal(t, map[string]any{"tag": "environment", "previous_value": nil, "value": ""}, data["context"])
	assert.Contains(t, data, "cluster")
	assert.NotContains(t, data, "ping")
	event.Type = "cluster.deleted"
	event.Tag = nil
	encoded, err = json.Marshal(eventPayload(event))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, &data))
	var tombstone map[string]any
	require.NoError(t, json.Unmarshal(encoded, &tombstone))
	assert.NotContains(t, tombstone, "cluster")
	assert.Equal(t, id.String(), tombstone["id"])
}

func TestUpgradePayloadDoesNotExposeInternalMessages(t *testing.T) {
	event := strategies.KubernetesEvent{
		Type: "cluster.upgrade.progress", Timestamp: time.Now(),
		Resource: &strategies.KubernetesResourceObservation{Cluster: &strategies.ClusterInfo{IsUpgrading: true}},
		Upgrade:  &strategies.UpgradeOperation{ID: "upgrade-operation", Version: "v1.36.5", Locked: true, Message: "sensitive internal diagnostic"},
	}
	encoded, err := json.Marshal(eventPayload(event))
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "sensitive internal diagnostic")
	var data map[string]any
	require.NoError(t, json.Unmarshal(encoded, &data))
	context := data["context"].(map[string]any)
	assert.Equal(t, "upgrade-operation", context["operation_id"])
	assert.Equal(t, "v1.36.5", context["target_version"])
	assert.Equal(t, "cluster", context["group"])
}

func TestEventContextJSONContract(t *testing.T) {
	for _, test := range []struct {
		name    string
		context EventContext
		want    string
	}{
		{
			name:    "cleared description",
			context: DescriptionChangedContext{PreviousDescription: "old description"},
			want:    `{"previous_description":"old description","description":""}`,
		},
		{
			name:    "unavailable kubeconfig",
			context: KubeconfigContext{Available: false},
			want:    `{"available":false}`,
		},
		{
			name:    "scaling without upgrade fields",
			context: ScalingContext{Group: "cluster", ControlPlaneStatus: &NodeStatus{}, WorkerStatus: &NodeStatus{}},
			want:    `{"group":"cluster","control_plane_status":{},"worker_status":{}}`,
		},
		{
			name: "upgrade with flattened replica statuses",
			context: UpgradeContext{
				ScalingContext: ScalingContext{Group: "cluster", ControlPlaneStatus: &NodeStatus{}, WorkerStatus: &NodeStatus{}},
				OperationID:    "operation", TargetVersion: "v1.36.5",
			},
			want: `{"group":"cluster","control_plane_status":{},"worker_status":{},"operation_id":"operation","target_version":"v1.36.5"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.context)
			require.NoError(t, err)
			assert.JSONEq(t, test.want, string(encoded))
		})
	}

	encoded, err := json.Marshal(KubernetesEventPayload{})
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(encoded, &payload))
	assert.NotContains(t, payload, "context")
}
