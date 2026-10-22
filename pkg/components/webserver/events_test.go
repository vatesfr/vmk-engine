package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	eventmanager "capv-proxy/pkg/components/event_manager"
	versionmanager "capv-proxy/pkg/components/version_manager"
	"capv-proxy/pkg/strategies"
	"capv-proxy/pkg/strategies/mocks"
)

const kubernetesEventsPath = "/kubernetes/events"

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (r *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	r.deadlines = append(r.deadlines, deadline)
	return nil
}

func TestKubernetesEventsRouterPreservesWriteDeadlines(t *testing.T) {
	controller := gomock.NewController(t)
	service := mocks.NewMockForManagingClusters(controller)
	events := mocks.NewMockForWatchingKubernetesEvents(controller)
	closed := make(chan strategies.KubernetesEvent)
	close(closed)
	events.EXPECT().WatchKubernetesEvents(gomock.Any()).Return(closed, nil)
	handler := (HttpServer{SwaggerUI: SwaggerUIDisabled}).Handler(service, nil, versionmanager.NewVersionManager(), events)
	writer := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, kubernetesEventsPath, nil))
	require.NotEmpty(t, writer.deadlines, "middleware must expose deadline support to the SSE handler")
	assert.True(t, writer.deadlines[0].After(time.Now()))
	assert.True(t, writer.deadlines[len(writer.deadlines)-1].IsZero(), "clear the deadline after flushing headers")
	assert.Equal(t, http.StatusOK, writer.Code)
	assert.True(t, writer.Flushed)
}

type heartbeatRecorder struct {
	*httptest.ResponseRecorder
	mu      sync.Mutex
	flushes int
}

func (r *heartbeatRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushes++
	r.ResponseRecorder.Flush()
}

func (r *heartbeatRecorder) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ResponseRecorder.Write(data)
}

func (r *heartbeatRecorder) bodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Body.String()
}

func (r *heartbeatRecorder) flushCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushes
}

func TestKubernetesEventsHeartbeat(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service := mocks.NewMockForManagingClusters(gomock.NewController(t))
		handler := (HttpServer{SwaggerUI: SwaggerUIDisabled}).Handler(service, nil, versionmanager.NewVersionManager(), eventmanager.NewEventManager())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		request := httptest.NewRequest(http.MethodGet, kubernetesEventsPath, nil).WithContext(ctx)
		response := &heartbeatRecorder{ResponseRecorder: httptest.NewRecorder()}
		done := make(chan struct{})
		start := time.Now()
		go func() {
			handler.ServeHTTP(response, request)
			close(done)
		}()

		synctest.Wait()
		require.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "text/event-stream", response.Header().Get("Content-Type"))
		assert.Equal(t, "no-cache, no-transform", response.Header().Get("Cache-Control"))
		assert.Equal(t, "no", response.Header().Get("X-Accel-Buffering"))
		assert.Equal(t, 1, response.flushCount(), "headers are sent immediately")
		assert.Empty(t, response.bodyString())

		time.Sleep(4 * time.Second)
		synctest.Wait()
		assert.Empty(t, response.bodyString(), "no heartbeat before five seconds")
		time.Sleep(time.Second)
		synctest.Wait()
		first := fmt.Sprintf("event: ping\ndata: {\"ping\":%d}\n\n", start.Add(5*time.Second).UnixMilli())
		assert.Equal(t, first, response.bodyString())
		assert.Equal(t, 2, response.flushCount())

		// Continue beyond Fuego's default 30-second response lifetime.
		for tick := 2; tick <= 7; tick++ {
			time.Sleep(5 * time.Second)
			synctest.Wait()
		}
		assert.Equal(t, 8, response.flushCount())
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("stream did not stop after cancellation")
		}
		body := response.bodyString()
		time.Sleep(10 * time.Second)
		synctest.Wait()
		assert.Equal(t, body, response.bodyString(), "no writes after disconnection")
	})
}

func TestKubernetesEventsRejectedRequests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		err    error
		status int
	}{
		{"backend failure", http.MethodGet, errTest, http.StatusInternalServerError},
		{"post", http.MethodPost, nil, http.StatusMethodNotAllowed},
		{"head", http.MethodHead, nil, http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := gomock.NewController(t)
			service := mocks.NewMockForManagingClusters(controller)
			events := mocks.NewMockForWatchingKubernetesEvents(controller)
			if tc.method == http.MethodGet {
				events.EXPECT().WatchKubernetesEvents(gomock.Any()).Return(nil, tc.err)
			}
			handler := (HttpServer{SwaggerUI: SwaggerUIDisabled}).Handler(service, nil, versionmanager.NewVersionManager(), events)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, kubernetesEventsPath, nil))
			assert.Equal(t, tc.status, response.Code)
			assert.NotEqual(t, "text/event-stream", response.Header().Get("Content-Type"))
		})
	}
}

func TestKubernetesEventsOpenAPI(t *testing.T) {
	service := mocks.NewMockForManagingClusters(gomock.NewController(t))
	server := (HttpServer{SwaggerUI: SwaggerUIDisabled}).newServer(service, nil, versionmanager.NewVersionManager(), eventmanager.NewEventManager())
	assert.Zero(t, server.WriteTimeout, "an absolute write timeout would terminate SSE streams")
	operation := server.OpenAPI.Description().Paths.Find("/kubernetes/events").Get
	require.NotNil(t, operation)
	content := operation.Responses.Status(http.StatusOK).Value.Content
	require.Contains(t, content, "text/event-stream")
	assert.NotContains(t, content, "application/json")
	assert.Empty(t, operation.Parameters, "the global stream has no cluster path parameter")
	assert.Nil(t, operation.Responses.Status(http.StatusNotFound))
	assert.Nil(t, server.OpenAPI.Description().Paths.Find("/kubernetes/clusters/{id}/events"))
}

func TestOldClusterEventsRouteIsNotRegistered(t *testing.T) {
	service := mocks.NewMockForManagingClusters(gomock.NewController(t))
	handler := (HttpServer{SwaggerUI: SwaggerUIDisabled}).Handler(service, nil, versionmanager.NewVersionManager(), eventmanager.NewEventManager())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/kubernetes/clusters/demo/events", nil))
	assert.Equal(t, http.StatusNotFound, response.Code)
}

type eventSourceFunc func(context.Context, func(strategies.KubernetesResourceChange)) (func(), error)

func (f eventSourceFunc) ObserveKubernetesResources(ctx context.Context, handle func(strategies.KubernetesResourceChange)) (func(), error) {
	return f(ctx, handle)
}

func TestBusinessEventsThroughHTTP(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		manager := eventmanager.NewEventManager()
		var publish func(strategies.KubernetesResourceChange)
		stop, err := manager.Start(ctx, eventSourceFunc(func(_ context.Context, handle func(strategies.KubernetesResourceChange)) (func(), error) {
			publish = handle
			return func() {}, nil
		}))
		require.NoError(t, err)
		defer stop()
		service := mocks.NewMockForManagingClusters(gomock.NewController(t))
		handler := (HttpServer{SwaggerUI: SwaggerUIDisabled}).Handler(service, nil, versionmanager.NewVersionManager(), manager)
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, kubernetesEventsPath, nil).WithContext(ctx))
			close(done)
		}()
		synctest.Wait()
		before := &strategies.KubernetesResourceObservation{
			Kind: strategies.ObservedCluster, Namespace: "management", Name: "http-events", UID: "cluster-resource-uid",
			Cluster: &strategies.ClusterInfo{ID: clusterUUID},
		}
		publish(strategies.KubernetesResourceChange{Current: before, Initial: true})
		synctest.Wait()
		assert.Empty(t, response.Body.String())
		publish(strategies.KubernetesResourceChange{Current: before})
		after := *before
		cluster := *before.Cluster
		after.Cluster = &cluster
		cluster.Paused = true
		cluster.Tags = map[string]string{"environment": ""}
		publish(strategies.KubernetesResourceChange{Previous: before, Current: &after})
		synctest.Wait()
		frames := strings.Split(strings.TrimSpace(response.Body.String()), "\n\n")
		require.Len(t, frames, 3)
		assert.True(t, strings.HasPrefix(frames[0], "event: cluster.created\n"))
		assert.True(t, strings.HasPrefix(frames[1], "event: cluster.reconciliation.paused\n"))
		assert.True(t, strings.HasPrefix(frames[2], "event: cluster.tag.created\n"))
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(strings.SplitN(frames[2], "data: ", 2)[1]), &payload))
		assert.Equal(t, clusterUUID.String(), payload["id"])
		assert.Equal(t, "cluster-resource-uid", payload["uid"])
		assert.Equal(t, map[string]any{"tag": "environment", "previous_value": nil, "value": ""}, payload["context"])
		cancel()
		synctest.Wait()
		<-done
	})
}
