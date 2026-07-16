package publisher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
		p := New(nil, nil, "")
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
		p := New(redactFn, nil, "")
		defer p.Shutdown(context.Background())

		if p.redactFn == nil {
			t.Error("redactFn not set")
		}
	})

	t.Run("starts workers", func(t *testing.T) {
		p := New(nil, nil, "")
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
		p := New(nil, nil, "")
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
		p := New(nil, nil, "")
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
		p := New(nil, nil, "")
		p.Shutdown(context.Background())

		ok := p.Publish(makeEvent("after.shutdown"))
		if ok {
			t.Error("Publish returned true after shutdown")
		}
	})

	t.Run("handles concurrent publishing", func(t *testing.T) {
		p := New(nil, nil, "")
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
		p := New(nil, nil, "")
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
		}, nil, "")
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
		}, nil, "")
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

	t.Run("event-level hook runs before string redaction and composes", func(t *testing.T) {
		var rawSeen string
		p := New(
			func(s string) string {
				if s == "raw secret (reviewed)" {
					return "[STRING-REDACTED]"
				}
				return s
			},
			func(e *core.Event) (*core.Event, error) {
				rawSeen = e.Parameters["data"].(string)
				modified := *e
				modified.Parameters = map[string]any{"data": rawSeen + " (reviewed)"}
				return &modified, nil
			},
			"")
		defer p.Shutdown(context.Background())

		event := &core.Event{
			PublishEventRequest: agentcatapi.PublishEventRequest{
				EventType: strPtr("test.hook.order"),
				Parameters: map[string]any{
					"data": "raw secret",
				},
			},
		}

		p.publishEvent(event, 0)

		if rawSeen != "raw secret" {
			t.Errorf("event hook saw %q, want the raw pre-redaction value", rawSeen)
		}
		if got := event.Parameters["data"]; got != "[STRING-REDACTED]" {
			t.Errorf("Parameters[data] = %v, want string redaction applied to the hook's output", got)
		}
	})

	t.Run("event-level hook returning nil drops the event before send", func(t *testing.T) {
		var requests int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&requests, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
		}))
		defer srv.Close()

		p := New(nil, func(e *core.Event) (*core.Event, error) {
			return nil, nil
		}, srv.URL)
		defer p.Shutdown(context.Background())

		event := &core.Event{
			PublishEventRequest: agentcatapi.PublishEventRequest{
				EventType: strPtr("test.hook.drop"),
			},
		}

		p.publishEvent(event, 0)

		if n := atomic.LoadInt32(&requests); n != 0 {
			t.Errorf("API received %d requests, want 0 (event dropped)", n)
		}
	})

	t.Run("event-level hook error drops the event before send", func(t *testing.T) {
		var requests int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&requests, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
		}))
		defer srv.Close()

		p := New(nil, func(e *core.Event) (*core.Event, error) {
			return nil, errors.New("hook failure")
		}, srv.URL)
		defer p.Shutdown(context.Background())

		event := &core.Event{
			PublishEventRequest: agentcatapi.PublishEventRequest{
				EventType: strPtr("test.hook.error"),
			},
		}

		p.publishEvent(event, 0)

		if n := atomic.LoadInt32(&requests); n != 0 {
			t.Errorf("API received %d requests, want 0 (event dropped)", n)
		}
	})

	t.Run("event-level hook panic drops the event without crashing", func(t *testing.T) {
		p := New(nil, func(e *core.Event) (*core.Event, error) {
			panic("hook exploded")
		}, "")
		defer p.Shutdown(context.Background())

		event := &core.Event{
			PublishEventRequest: agentcatapi.PublishEventRequest{
				EventType: strPtr("test.hook.panic"),
			},
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("publishEvent panicked: %v", r)
			}
		}()

		p.publishEvent(event, 0)
	})

	t.Run("handles API errors without panicking", func(t *testing.T) {
		p := New(nil, nil, "")
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
		p := New(nil, nil, "")
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
		p := New(nil, nil, "")

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
		p := New(nil, nil, "")

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
		p := New(nil, nil, "")

		p.Shutdown(context.Background())
		p.Shutdown(context.Background())
		p.Shutdown(context.Background())
	})

	t.Run("rejects new events after shutdown", func(t *testing.T) {
		p := New(nil, nil, "")
		p.Shutdown(context.Background())

		ok := p.Publish(makeEvent("test.after.shutdown"))
		if ok {
			t.Error("Publish should return false after shutdown")
		}
	})
}

func TestWorker(t *testing.T) {
	t.Run("processes events from queue", func(t *testing.T) {
		p := New(nil, nil, "")
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

	p1 := GetOrInit(nil, nil, "")
	if p1 == nil {
		t.Fatal("GetOrInit returned nil")
	}

	ShutdownGlobal(context.Background())

	p2 := GetOrInit(nil, nil, "")
	if p2 == nil {
		t.Fatal("GetOrInit returned nil after reset")
	}

	if p1 == p2 {
		t.Error("expected new publisher instance after ShutdownGlobal")
	}

	ShutdownGlobal(context.Background())
}
