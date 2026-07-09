package publisher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.agentcat.com/sdk/internal/core"
)

// newTelemetryTestServer returns an httptest server counting OTLP hits.
func newTelemetryTestServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func waitForHits(t *testing.T, hits *atomic.Int64, n int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if hits.Load() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("telemetry hits = %d, want >= %d", hits.Load(), n)
}

func TestPublishEvent_FansOutToExportersAlongsideAPISend(t *testing.T) {
	srv, hits := newTelemetryTestServer(t)

	p := New(nil, "", map[string]core.ExporterConfig{
		"otlp": {Type: "otlp", Endpoint: srv.URL},
	})
	defer p.Shutdown(context.Background())

	var apiSends atomic.Int64
	p.sendFn = func(event *core.Event, workerID int) bool {
		apiSends.Add(1)
		return true
	}

	p.publishEvent(makeEvent("telemetry.dual"), 0)

	if apiSends.Load() != 1 {
		t.Errorf("API sends = %d, want 1", apiSends.Load())
	}
	waitForHits(t, hits, 1, 3*time.Second)
}

func TestPublishEvent_TelemetryOnlySkipsAPISend(t *testing.T) {
	srv, hits := newTelemetryTestServer(t)

	p := New(nil, "", map[string]core.ExporterConfig{
		"otlp": {Type: "otlp", Endpoint: srv.URL},
	})
	defer p.Shutdown(context.Background())

	var apiSends atomic.Int64
	p.sendFn = func(event *core.Event, workerID int) bool {
		apiSends.Add(1)
		return true
	}

	evt := makeEvent("telemetry.only")
	evt.ProjectId = "" // telemetry-only mode
	p.publishEvent(evt, 0)

	waitForHits(t, hits, 1, 3*time.Second)
	if apiSends.Load() != 0 {
		t.Errorf("API sends = %d, want 0 in telemetry-only mode", apiSends.Load())
	}
}

func TestPublishEvent_ExporterFailureDoesNotAffectAPISend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	p := New(nil, "", map[string]core.ExporterConfig{
		"otlp": {Type: "otlp", Endpoint: srv.URL},
	})
	defer p.Shutdown(context.Background())

	var apiSends atomic.Int64
	p.sendFn = func(event *core.Event, workerID int) bool {
		apiSends.Add(1)
		return true
	}

	p.publishEvent(makeEvent("telemetry.fail"), 0)

	if apiSends.Load() != 1 {
		t.Errorf("API sends = %d, want 1 even when the exporter fails", apiSends.Load())
	}
}

func TestGetOrInit_ReplacesTelemetryManagerOnExistingPublisher(t *testing.T) {
	// Ensure a clean slate for the global publisher.
	ShutdownGlobal(context.Background())
	t.Cleanup(func() { ShutdownGlobal(context.Background()) })

	p1 := GetOrInit(nil, "", nil)
	if p1.telemetry.Load() != nil {
		t.Fatal("expected no telemetry manager without exporter configs")
	}

	srv, hits := newTelemetryTestServer(t)
	p2 := GetOrInit(nil, "", map[string]core.ExporterConfig{
		"otlp": {Type: "otlp", Endpoint: srv.URL},
	})
	if p2 != p1 {
		t.Fatal("GetOrInit should return the existing publisher")
	}
	tm := p2.telemetry.Load()
	if tm == nil || tm.Count() != 1 {
		t.Fatal("expected telemetry manager to be installed on the existing publisher")
	}

	var apiSends atomic.Int64
	p2.sendFn = func(event *core.Event, workerID int) bool {
		apiSends.Add(1)
		return true
	}
	p2.publishEvent(makeEvent("telemetry.late"), 0)
	waitForHits(t, hits, 1, 3*time.Second)
}
