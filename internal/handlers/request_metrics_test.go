package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestMetricsTrackInFlightCurrentAndMaximum(t *testing.T) {
	metrics := &RequestMetrics{}
	entered := make(chan struct{})
	release := make(chan struct{})
	handler := metrics.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		close(done)
	}()
	<-entered

	active := metrics.Snapshot()
	if active.InFlight != 1 || active.MaxInFlight != 1 || active.Total != 1 {
		t.Fatalf("active metrics = %#v", active)
	}
	close(release)
	<-done
	finished := metrics.Snapshot()
	if finished.InFlight != 0 || finished.MaxInFlight != 1 || finished.Total != 1 {
		t.Fatalf("finished metrics = %#v", finished)
	}
}
