package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"

	eventmanager "capv-proxy/pkg/components/event_manager"
	"capv-proxy/pkg/strategies"
)

type failedStreamWriter struct {
	*httptest.ResponseRecorder
	failFlush bool
	writes    int
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
