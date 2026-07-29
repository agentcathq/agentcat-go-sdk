package publisher

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.agentcat.com/sdk/internal/core"
)

// TestWorker_SurvivesPanicWhileSending verifies the worker's top-level panic
// recovery: a panic while publishing one event is logged and dropped, and the
// same worker keeps processing later events. Without the recovery this test
// crashes the whole test process.
func TestWorker_SurvivesPanicWhileSending(t *testing.T) {
	p := New(nil, "http://localhost:0", nil)
	p.maxRetries = 0
	defer p.Shutdown(context.Background())

	var sent atomic.Int64
	p.sendFn = func(event *core.Event, workerID int) bool {
		if event.GetEventType() == "test.poison" {
			panic("simulated bug while sending")
		}
		sent.Add(1)
		return true
	}

	// Enqueue more poison events than there are workers so every worker
	// goroutine hits at least one panic, then verify normal events still flow.
	for range MaxWorkers * 2 {
		p.Publish(makeEvent("test.poison"))
	}
	for range 10 {
		p.Publish(makeEvent("test.normal"))
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sent.Load() >= 10 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("workers stopped processing after panics: sent %d of 10 normal events", sent.Load())
}

// TestPublishEvent_HostileParameters runs the full pipeline (redact ->
// sanitize -> truncate -> send) over adversarial customer payloads: channels,
// funcs, NaN/Inf floats, and self-referential maps. The pipeline must not
// panic and must leave the event JSON-marshalable for the API send.
func TestPublishEvent_HostileParameters(t *testing.T) {
	cyclic := map[string]any{"name": "cycle"}
	cyclic["self"] = cyclic

	hostile := map[string]any{
		"channel":  make(chan int),
		"function": func() {},
		"nan":      math.NaN(),
		"posInf":   math.Inf(1),
		"negInf":   math.Inf(-1),
		"cycle":    cyclic,
		"nilMap":   map[string]any(nil),
		"normal":   "keep-me",
	}

	p := New(func(s string) string { return s }, "http://localhost:0", nil)
	p.maxRetries = 0
	defer p.Shutdown(context.Background())

	var captured *core.Event
	var mu sync.Mutex
	p.sendFn = func(event *core.Event, workerID int) bool {
		mu.Lock()
		captured = event
		mu.Unlock()
		return true
	}

	event := makeEvent("test.hostile")
	event.Parameters = hostile
	event.IdentifyData = map[string]any{"deep": cyclic, "bad": math.NaN()}

	p.publishEvent(event, 0)

	mu.Lock()
	defer mu.Unlock()
	if captured == nil {
		t.Fatal("event was not sent")
	}
	if _, err := json.Marshal(captured.PublishEventRequest); err != nil {
		t.Fatalf("event is not JSON-marshalable after pipeline: %v", err)
	}
	if captured.Parameters["normal"] != "keep-me" {
		t.Errorf("normal value lost: %v", captured.Parameters["normal"])
	}
}

// TestShutdown_HangingExporterBoundedByDeadline verifies that a telemetry
// exporter endpoint that never responds cannot block Shutdown beyond its
// context deadline.
func TestShutdown_HangingExporterBoundedByDeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // hang until the test finishes
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	p := New(nil, "http://localhost:0", map[string]core.ExporterConfig{
		"hang": {Type: "otlp", Endpoint: srv.URL},
	})
	p.maxRetries = 0
	p.sendFn = func(event *core.Event, workerID int) bool { return true }

	p.Publish(makeEvent("test.hang"))

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	p.Shutdown(ctx)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Shutdown blocked on hanging exporter for %v", elapsed)
	}
}
