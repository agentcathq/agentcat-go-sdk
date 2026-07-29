package publisher

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
)

func strPtr(s string) *string {
	return &s
}

func TestNew(t *testing.T) {
	t.Run("creates publisher with default configuration", func(t *testing.T) {
		p := New(nil, "", nil)
		defer p.Shutdown(context.Background())

		if p == nil {
			t.Fatal("New() returned nil")
		}
		if p.queue == nil {
			t.Error("queue channel not initialized")
		}
		if cap(p.queue) != QueueSize {
			t.Errorf("queue capacity = %d, want %d", cap(p.queue), QueueSize)
		}
		if p.apiClient == nil {
			t.Error("apiClient not initialized")
		}
		if p.logger == nil {
			t.Error("logger not initialized")
		}
		if p.shutdownCh == nil {
			t.Error("shutdownCh not initialized")
		}
	})

	t.Run("creates publisher with redact function", func(t *testing.T) {
		redactFn := func(s string) string { return "***" }
		p := New(redactFn, "", nil)
		defer p.Shutdown(context.Background())

		if p.redactFn == nil {
			t.Error("redactFn not set")
		}
	})

	t.Run("starts workers", func(t *testing.T) {
		p := New(nil, "", nil)
		defer p.Shutdown(context.Background())

		time.Sleep(50 * time.Millisecond)

		event := &core.Event{
			PublishEventRequest: agentcatapi.PublishEventRequest{
				EventType: strPtr("test"),
			},
		}

		p.Publish(event)

		if len(p.queue) > QueueSize {
			t.Error("workers not processing events")
		}
	})
}

func TestPublish(t *testing.T) {
	t.Run("successfully enqueues event", func(t *testing.T) {
		p := New(nil, "", nil)
		defer p.Shutdown(context.Background())

		event := &core.Event{
			PublishEventRequest: agentcatapi.PublishEventRequest{
				EventType: strPtr("test.event"),
			},
		}

		ok := p.Publish(event)
		if !ok {
			t.Error("Publish returned false, expected true")
		}

		time.Sleep(50 * time.Millisecond)

		queueLen := len(p.queue)
		if queueLen >= QueueSize {
			t.Errorf("queue length = %d, expected < %d", queueLen, QueueSize)
		}
	})

	t.Run("handles nil event gracefully", func(t *testing.T) {
		p := New(nil, "", nil)
		defer p.Shutdown(context.Background())

		ok := p.Publish(nil)
		if ok {
			t.Error("Publish returned true for nil event")
		}

		time.Sleep(50 * time.Millisecond)
		if len(p.queue) != 0 {
			t.Error("nil event was enqueued")
		}
	})

	t.Run("drops events when queue is full", func(t *testing.T) {
		p, _ := newSpyPublisher(0, 5, nil) // 0 workers so nothing gets consumed

		for i := 0; i < 5; i++ {
			p.Publish(makeEvent("fill"))
		}

		ok := p.Publish(makeEvent("dropped"))
		if ok {
			t.Error("Publish returned true for dropped event")
		}

		// Clean up: close the channel so test doesn't leak
		p.closeMu.Lock()
		p.closed = true
		close(p.queue)
		p.closeMu.Unlock()
	})

	t.Run("rejects events after shutdown", func(t *testing.T) {
		p := New(nil, "", nil)
		p.Shutdown(context.Background())

		ok := p.Publish(makeEvent("after.shutdown"))
		if ok {
			t.Error("Publish returned true after shutdown")
		}
	})

	t.Run("handles concurrent publishing", func(t *testing.T) {
		p := New(nil, "", nil)
		defer p.Shutdown(context.Background())

		var wg sync.WaitGroup
		numGoroutines := 10
		eventsPerGoroutine := 5

		wg.Add(numGoroutines)
		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer wg.Done()
				for j := 0; j < eventsPerGoroutine; j++ {
					p.Publish(makeEvent("concurrent.test"))
				}
			}()
		}

		wg.Wait()
		time.Sleep(200 * time.Millisecond)
	})
}

func TestPublishEvent(t *testing.T) {
	t.Run("does not panic on publish", func(t *testing.T) {
		p := New(nil, "", nil)
		p.maxRetries = 0 // avoid retry backoff against the real API in tests
		defer p.Shutdown(context.Background())

		event := &core.Event{
			PublishEventRequest: agentcatapi.PublishEventRequest{
				EventType: strPtr("test.success"),
			},
		}

		p.publishEvent(event, 0)
	})

	t.Run("applies redaction before publishing", func(t *testing.T) {
		p := New(func(s string) string {
			if s == "secret" {
				return "***"
			}
			return s
		}, "", nil)
		p.maxRetries = 0
		defer p.Shutdown(context.Background())

		event := &core.Event{
			PublishEventRequest: agentcatapi.PublishEventRequest{
				EventType: strPtr("test.redaction"),
				Parameters: map[string]any{
					"data": "secret",
				},
			},
		}

		p.publishEvent(event, 0)

		if event.Parameters["data"] != "***" {
			t.Errorf("redaction not applied: got %v, want ***", event.Parameters["data"])
		}
	})

	t.Run("handles redaction errors gracefully", func(t *testing.T) {
		p := New(func(s string) string {
			panic("redaction panic")
		}, "", nil)
		p.maxRetries = 0
		defer p.Shutdown(context.Background())

		event := &core.Event{
			PublishEventRequest: agentcatapi.PublishEventRequest{
				EventType: strPtr("test.redaction.error"),
				Parameters: map[string]any{
					"data": "value",
				},
			},
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("publishEvent panicked: %v", r)
			}
		}()

		p.publishEvent(event, 0)

		if event.Parameters["data"] != "[REDACTION_ERROR]" {
			t.Errorf("event was not sanitized: got %v, want [REDACTION_ERROR]", event.Parameters["data"])
		}
	})

	t.Run("handles API errors without panicking", func(t *testing.T) {
		p := New(nil, "", nil)
		p.maxRetries = 0
		defer p.Shutdown(context.Background())

		event := &core.Event{
			PublishEventRequest: agentcatapi.PublishEventRequest{
				EventType: strPtr("test.api.error"),
			},
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("publishEvent panicked on API error: %v", r)
			}
		}()

		p.publishEvent(event, 0)
	})

	t.Run("respects context timeout", func(t *testing.T) {
		p := New(nil, "", nil)
		p.maxRetries = 0
		defer p.Shutdown(context.Background())

		event := &core.Event{
			PublishEventRequest: agentcatapi.PublishEventRequest{
				EventType: strPtr("test.timeout"),
			},
		}

		done := make(chan bool)
		go func() {
			p.publishEvent(event, 0)
			done <- true
		}()

		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("publishEvent did not respect timeout")
		}
	})
}

func TestShutdown(t *testing.T) {
	t.Run("shuts down cleanly with empty queue", func(t *testing.T) {
		p := New(nil, "", nil)

		if err := p.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown returned error: %v", err)
		}

		select {
		case <-p.shutdownCh:
		case <-time.After(100 * time.Millisecond):
			t.Error("shutdownCh was not closed")
		}
	})

	t.Run("drains queue before shutdown completes", func(t *testing.T) {
		p := New(nil, "", nil)

		for i := 0; i < 5; i++ {
			p.Publish(makeEvent("test.shutdown"))
		}

		time.Sleep(50 * time.Millisecond)

		if err := p.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown returned error: %v", err)
		}
	})

	t.Run("handles shutdown timeout", func(t *testing.T) {
		slowHandler := func(_ *core.Event) {
			time.Sleep(100 * time.Millisecond)
		}

		p, _ := newSpyPublisher(1, QueueSize, slowHandler)

		for i := 0; i < 50; i++ {
			p.Publish(makeEvent("slow.test"))
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()
		time.Sleep(1 * time.Millisecond)

		start := time.Now()
		err := p.Shutdown(ctx)
		elapsed := time.Since(start)

		if err == nil {
			t.Error("expected Shutdown to return a timeout error")
		}

		if elapsed > 2*time.Second {
			t.Errorf("shutdown took too long = %v, want < 2s", elapsed)
		}
	})

	t.Run("can be called multiple times safely", func(t *testing.T) {
		p := New(nil, "", nil)

		p.Shutdown(context.Background())
		p.Shutdown(context.Background())
		p.Shutdown(context.Background())
	})

	t.Run("rejects new events after shutdown", func(t *testing.T) {
		p := New(nil, "", nil)
		p.Shutdown(context.Background())

		ok := p.Publish(makeEvent("test.after.shutdown"))
		if ok {
			t.Error("Publish should return false after shutdown")
		}
	})
}

func TestWorker(t *testing.T) {
	t.Run("processes events from queue", func(t *testing.T) {
		p := New(nil, "", nil)
		defer p.Shutdown(context.Background())

		p.Publish(makeEvent("test.worker"))

		time.Sleep(200 * time.Millisecond)
	})

	t.Run("stops when channel is closed", func(t *testing.T) {
		p, counter := newSpyPublisher(MaxWorkers, QueueSize, nil)

		p.Publish(makeEvent("test"))

		time.Sleep(50 * time.Millisecond)

		p.Shutdown(context.Background())

		processed := atomic.LoadInt64(counter)
		if processed < 1 {
			t.Errorf("expected at least 1 processed event, got %d", processed)
		}
	})
}

// newSpyPublisher creates a Publisher whose workers increment an atomic counter
// instead of making real API calls.
func newSpyPublisher(numWorkers int, queueSize int, handler func(*core.Event)) (*Publisher, *int64) {
	var counter int64

	p := &Publisher{
		queue:      make(chan *core.Event, queueSize),
		logger:     logging.New(),
		shutdownCh: make(chan struct{}),
	}

	for i := 0; i < numWorkers; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for event := range p.queue {
				if event != nil {
					if handler != nil {
						handler(event)
					}
					atomic.AddInt64(&counter, 1)
				}
			}
		}()
	}

	return p, &counter
}

func makeEvent(eventType string) *core.Event {
	return &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			EventType: strPtr(eventType),
			// A project ID is required for the API send path; without one the
			// publisher treats the event as telemetry-only and skips sending.
			ProjectId: "proj_test",
		},
	}
}

func TestShutdownDrainsAllQueuedEvents(t *testing.T) {
	const totalEvents = 20

	p, counter := newSpyPublisher(MaxWorkers, QueueSize, nil)

	for i := 0; i < totalEvents; i++ {
		p.Publish(makeEvent("drain.test"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := p.Shutdown(ctx)
	if err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	processed := atomic.LoadInt64(counter)
	if processed != totalEvents {
		t.Errorf("processed %d events, want %d", processed, totalEvents)
	}
}

func TestShutdownRespectsContextDeadline(t *testing.T) {
	slowHandler := func(_ *core.Event) {
		time.Sleep(100 * time.Millisecond)
	}

	p, _ := newSpyPublisher(1, QueueSize, slowHandler)

	for i := 0; i < 50; i++ {
		p.Publish(makeEvent("slow.test"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(1 * time.Millisecond)

	err := p.Shutdown(ctx)
	if err == nil {
		t.Fatal("expected Shutdown to return a non-nil error when context deadline exceeded")
	}

	if err != context.DeadlineExceeded && err != context.Canceled {
		t.Errorf("expected context.DeadlineExceeded or context.Canceled, got %v", err)
	}
}

func TestShutdownIsIdempotent(t *testing.T) {
	p, _ := newSpyPublisher(MaxWorkers, QueueSize, nil)

	for i := 0; i < 5; i++ {
		p.Publish(makeEvent("idempotent.test"))
	}

	err1 := p.Shutdown(context.Background())
	if err1 != nil {
		t.Errorf("first Shutdown returned error: %v", err1)
	}

	err2 := p.Shutdown(context.Background())
	if err2 != nil {
		t.Errorf("second Shutdown returned error: %v", err2)
	}
}

func TestWorkerDrainsOnChannelClose(t *testing.T) {
	const totalEvents = 15
	p, counter := newSpyPublisher(MaxWorkers, QueueSize, nil)

	for i := 0; i < totalEvents; i++ {
		p.Publish(makeEvent("drain.close.test"))
	}

	time.Sleep(10 * time.Millisecond)

	err := p.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	processed := atomic.LoadInt64(counter)
	if processed != totalEvents {
		t.Errorf("processed %d events after channel close, want %d", processed, totalEvents)
	}
}

func TestShutdownReturnsErrorWhenEventsRemain(t *testing.T) {
	slowHandler := func(_ *core.Event) {
		time.Sleep(500 * time.Millisecond)
	}

	p, _ := newSpyPublisher(1, QueueSize, slowHandler)

	for i := 0; i < 20; i++ {
		p.Publish(makeEvent("timeout.test"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := p.Shutdown(ctx)
	if err == nil {
		t.Fatal("expected Shutdown to return an error when events remain unprocessed")
	}

	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestGetOrInit_Resettable(t *testing.T) {
	globalMu.Lock()
	globalPub = nil
	globalMu.Unlock()

	p1 := GetOrInit(nil, "", nil)
	if p1 == nil {
		t.Fatal("GetOrInit returned nil")
	}

	ShutdownGlobal(context.Background())

	p2 := GetOrInit(nil, "", nil)
	if p2 == nil {
		t.Fatal("GetOrInit returned nil after reset")
	}

	if p1 == p2 {
		t.Error("expected new publisher instance after ShutdownGlobal")
	}

	ShutdownGlobal(context.Background())
}

func TestQueueSizeIncreased(t *testing.T) {
	if QueueSize != 10000 {
		t.Errorf("QueueSize = %d, want 10000", QueueSize)
	}
}

func TestPublishEvent_RetriesFailedSends(t *testing.T) {
	p := New(nil, "", nil)
	defer p.Shutdown(context.Background())

	var attempts int64
	p.sendFn = func(event *core.Event, workerID int) bool {
		n := atomic.AddInt64(&attempts, 1)
		return n >= 2 // fail once, succeed on first retry
	}

	start := time.Now()
	p.publishEvent(makeEvent("retry.test"), 0)
	elapsed := time.Since(start)

	if got := atomic.LoadInt64(&attempts); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
	// First retry should be delayed by ~1s of backoff.
	if elapsed < RetryBaseDelay {
		t.Errorf("expected at least %v of backoff, got %v", RetryBaseDelay, elapsed)
	}
}

func TestPublishEvent_GivesUpAfterMaxRetries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping backoff test in -short mode")
	}

	p := New(nil, "", nil)
	defer p.Shutdown(context.Background())

	var attempts int64
	p.sendFn = func(event *core.Event, workerID int) bool {
		atomic.AddInt64(&attempts, 1)
		return false
	}

	p.publishEvent(makeEvent("giveup.test"), 0)

	if got := atomic.LoadInt64(&attempts); got != int64(MaxRetries)+1 {
		t.Errorf("attempts = %d, want %d", got, MaxRetries+1)
	}
}

func TestPublishEvent_RetryInterruptedByShutdown(t *testing.T) {
	p := New(nil, "", nil)

	var attempts int64
	p.sendFn = func(event *core.Event, workerID int) bool {
		atomic.AddInt64(&attempts, 1)
		return false
	}

	done := make(chan struct{})
	go func() {
		p.publishEvent(makeEvent("interrupt.test"), 0)
		close(done)
	}()

	// Let the first attempt fail, then shut down during the backoff wait.
	time.Sleep(100 * time.Millisecond)
	p.Shutdown(context.Background())

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("publishEvent did not abort retries on shutdown")
	}

	if got := atomic.LoadInt64(&attempts); got != 1 {
		t.Errorf("attempts = %d, want 1 (retries should be interrupted)", got)
	}
}

func TestPublishEvent_AppliesSanitization(t *testing.T) {
	p := New(nil, "", nil)
	defer p.Shutdown(context.Background())

	var sent *core.Event
	p.sendFn = func(event *core.Event, workerID int) bool {
		sent = event
		return true
	}

	event := makeEvent("sanitize.test")
	event.Response = map[string]any{
		"content": []any{
			map[string]any{"type": "image", "data": "aGVsbG8=", "mimeType": "image/png"},
		},
	}

	p.publishEvent(event, 0)

	if sent == nil {
		t.Fatal("event was not sent")
	}
	block := sent.Response["content"].([]any)[0].(map[string]any)
	if block["type"] != "text" {
		t.Errorf("image block was not sanitized: %v", block)
	}
}

func TestPublishEvent_AppliesTruncation(t *testing.T) {
	p := New(nil, "", nil)
	defer p.Shutdown(context.Background())

	var sent *core.Event
	p.sendFn = func(event *core.Event, workerID int) bool {
		sent = event
		return true
	}

	longIntent := strings.Repeat("i", 5000)
	event := makeEvent("truncate.test")
	event.UserIntent = &longIntent

	p.publishEvent(event, 0)

	if sent == nil {
		t.Fatal("event was not sent")
	}
	if len(*sent.UserIntent) != 2048+len("...") {
		t.Errorf("user intent length = %d, want %d", len(*sent.UserIntent), 2048+len("..."))
	}
}

func TestPublishEvent_PipelineOrderRedactSanitizeTruncate(t *testing.T) {
	// Redaction must run before sanitization/truncation (matching the TS
	// pipeline). A redaction function that expands a marker into a long string
	// should be truncated afterwards.
	p := New(func(s string) string {
		if s == "EXPAND" {
			return strings.Repeat("x", 5000)
		}
		return s
	}, "", nil)
	defer p.Shutdown(context.Background())

	var sent *core.Event
	p.sendFn = func(event *core.Event, workerID int) bool {
		sent = event
		return true
	}

	intent := "EXPAND"
	event := makeEvent("pipeline.test")
	event.UserIntent = &intent

	p.publishEvent(event, 0)

	if sent == nil {
		t.Fatal("event was not sent")
	}
	if len(*sent.UserIntent) != 2048+len("...") {
		t.Errorf("user intent length = %d, want %d (redacted then truncated)",
			len(*sent.UserIntent), 2048+len("..."))
	}
}
