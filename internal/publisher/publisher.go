package publisher

import (
	"context"
	"sync"
	"time"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
	"go.agentcat.com/sdk/internal/redaction"
)

var (
	globalPub *Publisher
	globalMu  sync.Mutex
)

// GetOrInit returns the global publisher, creating it on first call.
// Unlike sync.Once, this allows re-initialization after ShutdownGlobal.
// If apiBaseURL is empty, the default MCPCat API URL is used.
func GetOrInit(redactFn core.RedactFunc, apiBaseURL string) *Publisher {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalPub == nil {
		globalPub = New(redactFn, apiBaseURL)
	}
	return globalPub
}

// ShutdownGlobal shuts down the global publisher and resets it so that
// GetOrInit can create a fresh instance on the next call.
func ShutdownGlobal(ctx context.Context) error {
	globalMu.Lock()
	pub := globalPub
	globalPub = nil
	globalMu.Unlock()

	if pub != nil {
		return pub.Shutdown(ctx)
	}
	return nil
}

// Publisher handles asynchronous event publishing to the MCPCat API.
type Publisher struct {
	queue        chan *core.Event
	apiClient    *agentcatapi.APIClient
	logger       *logging.Logger
	redactFn     core.RedactFunc
	wg           sync.WaitGroup
	closeMu      sync.Mutex
	closed       bool
	shutdownCh   chan struct{}
	shutdownOnce sync.Once
}

// New creates a new Publisher instance and starts worker goroutines.
// If apiBaseURL is empty, the default MCPCat API URL is used.
func New(redactFn core.RedactFunc, apiBaseURL string) *Publisher {
	logger := logging.New()

	baseURL := DefaultAPIBaseURL
	if apiBaseURL != "" {
		baseURL = apiBaseURL
	}

	cfg := agentcatapi.NewConfiguration()
	cfg.Servers = agentcatapi.ServerConfigurations{
		{
			URL:         baseURL,
			Description: "MCPCat API",
		},
	}

	apiClient := agentcatapi.NewAPIClient(cfg)

	p := &Publisher{
		queue:      make(chan *core.Event, QueueSize),
		apiClient:  apiClient,
		logger:     logger,
		redactFn:   redactFn,
		shutdownCh: make(chan struct{}),
	}

	for i := 0; i < MaxWorkers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	logger.Infof("Publisher started with %d workers and queue size %d", MaxWorkers, QueueSize)

	return p
}

// worker processes events from the queue until the channel is closed.
// Using range ensures all queued events are drained before the worker exits.
func (p *Publisher) worker(id int) {
	defer p.wg.Done()

	p.logger.Debugf("Worker %d started", id)

	for event := range p.queue {
		if event != nil {
			p.publishEvent(event, id)
		}
	}

	p.logger.Debugf("Worker %d stopped", id)
}

// publishEvent sends a single event to the MCPCat API.
func (p *Publisher) publishEvent(event *core.Event, workerID int) {
	if p.redactFn != nil {
		err := redaction.RedactEvent(event, p.redactFn)
		if err != nil {
			p.logger.Warnf("Worker %d redaction failed for event %s: %v - publishing with error placeholders",
				workerID, event.GetId(), err)
		} else {
			p.logger.Debugf("Worker %d applied redaction to event %s", workerID, event.GetId())
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, resp, err := p.apiClient.EventsAPI.PublishEvent(ctx).
		PublishEventRequest(event.PublishEventRequest).
		Execute()
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		p.logger.Errorf("Worker %d failed to publish event: %v", workerID, err)
		if resp != nil {
			p.logger.Debugf("Response status: %s", resp.Status)
		}
		return
	}

	if resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		p.logger.Debugf("Worker %d successfully published event %s", workerID, event.GetId())
	} else if resp != nil {
		p.logger.Warnf("Worker %d received unexpected status code: %d", workerID, resp.StatusCode)
	}
}

// Publish enqueues an event for publishing. Returns true if the event was
// accepted, false if it was dropped (nil event, queue full, or shutting down).
func (p *Publisher) Publish(event *core.Event) bool {
	if event == nil {
		return false
	}

	p.closeMu.Lock()
	if p.closed {
		p.closeMu.Unlock()
		p.logger.Warnf("Publish rejected (shutting down), dropping event %s", event.GetId())
		return false
	}

	select {
	case p.queue <- event:
		p.closeMu.Unlock()
		p.logger.Debugf("Event %s enqueued for publishing", event.GetId())
		return true
	default:
		p.closeMu.Unlock()
		p.logger.Warnf("Queue full, dropping event %s", event.GetId())
		return false
	}
}

// Shutdown gracefully shuts down the publisher, waiting for queued events to be
// published until the provided context is done. If ctx has no deadline, a
// default 5-second timeout is applied.
func (p *Publisher) Shutdown(ctx context.Context) error {
	var shutdownErr error
	p.shutdownOnce.Do(func() {
		queuedCount := len(p.queue)
		if queuedCount > 0 {
			p.logger.Infof("Publisher shutting down with %d events in queue...", queuedCount)
		} else {
			p.logger.Info("Publisher shutting down...")
		}

		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
		}

		// Close the queue under the lock so no concurrent Publish can write
		// to the closed channel. Workers will drain remaining events via range.
		p.closeMu.Lock()
		p.closed = true
		close(p.queue)
		p.closeMu.Unlock()

		done := make(chan struct{})
		go func() {
			p.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			p.logger.Info("All events published successfully")
		case <-ctx.Done():
			p.logger.Warnf("Shutdown timeout reached, some events may not have been published")
			shutdownErr = ctx.Err()
		}

		close(p.shutdownCh)
	})
	return shutdownErr
}
