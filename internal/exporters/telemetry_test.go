package exporters

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.agentcat.com/sdk/internal/core"
)

func TestNewManager_SkipsUnknownTypes(t *testing.T) {
	m := NewManager(map[string]core.ExporterConfig{
		"mystery": {Type: "opentelemetry-collector-deluxe"},
	})
	if m.Count() != 0 {
		t.Errorf("Count = %d, want 0 (unknown type skipped)", m.Count())
	}
}

func TestNewManager_SkipsInvalidConfigs(t *testing.T) {
	m := NewManager(map[string]core.ExporterConfig{
		"otlp":    {Type: "otlp"},                     // missing endpoint
		"datadog": {Type: "datadog", APIKey: "k"},     // missing site/service
		"sentry":  {Type: "sentry", DSN: "not-a-dsn"}, // invalid DSN
		"posthog": {Type: "posthog"},                  // missing apiKey
	})
	if m.Count() != 0 {
		t.Errorf("Count = %d, want 0 (all configs invalid)", m.Count())
	}
}

func TestNewManager_InitializesValidExporters(t *testing.T) {
	m := NewManager(map[string]core.ExporterConfig{
		"otlp":    {Type: "otlp", Endpoint: "http://collector:4318"},
		"posthog": {Type: "posthog", APIKey: "phc_x"},
		"unknown": {Type: "nope"},
	})
	if m.Count() != 2 {
		t.Errorf("Count = %d, want 2", m.Count())
	}
}

// countingExporter counts exports and optionally fails or panics.
type countingExporter struct {
	calls atomic.Int64
	err   error
	panic bool
}

func (c *countingExporter) Export(event *core.Event) error {
	c.calls.Add(1)
	if c.panic {
		panic("exporter bug")
	}
	return c.err
}

func TestManager_FanOutAndErrorIsolation(t *testing.T) {
	healthy := &countingExporter{}
	failing := &countingExporter{err: errors.New("backend down")}
	panicking := &countingExporter{panic: true}

	m := &Manager{
		exporters: map[string]core.Exporter{
			"healthy":   healthy,
			"failing":   failing,
			"panicking": panicking,
		},
		logger: testLogger(),
	}

	m.ExportAsync(testEvent())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.Wait(ctx)

	if healthy.calls.Load() != 1 {
		t.Errorf("healthy exporter calls = %d, want 1", healthy.calls.Load())
	}
	if failing.calls.Load() != 1 {
		t.Errorf("failing exporter calls = %d, want 1", failing.calls.Load())
	}
	if panicking.calls.Load() != 1 {
		t.Errorf("panicking exporter calls = %d, want 1", panicking.calls.Load())
	}
}

func TestManager_ExportAsyncNilEvent(t *testing.T) {
	healthy := &countingExporter{}
	m := &Manager{
		exporters: map[string]core.Exporter{"healthy": healthy},
		logger:    testLogger(),
	}

	m.ExportAsync(nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m.Wait(ctx)

	if healthy.calls.Load() != 0 {
		t.Errorf("calls = %d, want 0 for nil event", healthy.calls.Load())
	}
}

func TestManager_EndToEndFanOut(t *testing.T) {
	var otlpHits, posthogHits atomic.Int64
	otlpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otlpHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer otlpSrv.Close()
	posthogSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posthogHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer posthogSrv.Close()

	m := NewManager(map[string]core.ExporterConfig{
		"otlp":    {Type: "otlp", Endpoint: otlpSrv.URL},
		"posthog": {Type: "posthog", APIKey: "phc_x", Host: posthogSrv.URL},
	})
	if m.Count() != 2 {
		t.Fatalf("Count = %d, want 2", m.Count())
	}

	m.ExportAsync(testEvent())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.Wait(ctx)

	if otlpHits.Load() != 1 {
		t.Errorf("otlp hits = %d, want 1", otlpHits.Load())
	}
	if posthogHits.Load() != 1 {
		t.Errorf("posthog hits = %d, want 1", posthogHits.Load())
	}
}
