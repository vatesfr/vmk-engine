package webserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	eventmanager "capv-proxy/pkg/components/event_manager"
	versionmanager "capv-proxy/pkg/components/version_manager"
	"capv-proxy/pkg/strategies/mocks"
)

const kubernetesEventsPath = "/kubernetes/events"

type heartbeatRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (r *heartbeatRecorder) Flush() {
	r.flushes++
	r.ResponseRecorder.Flush()
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
		assert.Equal(t, 1, response.flushes, "headers are sent immediately")
		assert.Empty(t, response.Body.String())

		time.Sleep(4 * time.Second)
		synctest.Wait()
		assert.Empty(t, response.Body.String(), "no heartbeat before five seconds")
		time.Sleep(time.Second)
		synctest.Wait()
		first := fmt.Sprintf("event: ping\ndata: {\"ping\":%d}\n\n", start.Add(5*time.Second).UnixMilli())
		assert.Equal(t, first, response.Body.String())
		assert.Equal(t, 2, response.flushes)

		// Continue beyond Fuego's default 30-second response lifetime.
		for tick := 2; tick <= 7; tick++ {
			time.Sleep(5 * time.Second)
			synctest.Wait()
		}
		assert.Equal(t, 8, response.flushes)
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("stream did not stop after cancellation")
		}
		body := response.Body.String()
		time.Sleep(10 * time.Second)
		synctest.Wait()
		assert.Equal(t, body, response.Body.String(), "no writes after disconnection")
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
