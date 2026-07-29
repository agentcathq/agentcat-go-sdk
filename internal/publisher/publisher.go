package publisher

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/exporters"
	"go.agentcat.com/sdk/internal/logging"
	"go.agentcat.com/sdk/internal/redaction"
	"go.agentcat.com/sdk/internal/sanitization"
	"go.agentcat.com/sdk/internal/truncation"
)

var (
	globalPub *Publisher
	globalMu  sync.Mutex
)

// GetOrInit returns the global publisher, creating it on first call.
// Unlike sync.Once, this allows re-initialization after ShutdownGlobal.
// If apiBaseURL is empty, the default AgentCat API URL is used. When exporter
// configs are provided and the publisher already exists, its telemetry
// manager is replaced (mirroring the TypeScript SDK, where track() installs a
// fresh TelemetryManager on the shared event queue).
func GetOrInit(redactFn core.RedactFunc, apiBaseURL string, exporterConfigs map[string]core.ExporterConfig) *Publisher {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalPub == nil {
		globalPub = New(redactFn, apiBaseURL, exporterConfigs)
	} else if len(exporterConfigs) > 0 {
		globalPub.SetTelemetryManager(exporters.NewManager(exporterConfigs))
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

// Publisher handles asynchronous event publishing to the AgentCat API and
// fan-out to configured telemetry exporters.
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

	// telemetry fans events out to configured exporters. It is replaceable at
	// runtime (see GetOrInit) and may be nil when no exporters are configured.
	telemetry atomic.Pointer[exporters.Manager]

	// maxRetries is the number of retries after a failed send (MaxRetries by
	// default; overridable in tests).
	maxRetries int

	// sendFn performs a single send attempt and reports success. It defaults
	// to sendEvent; tests may replace it to avoid real API calls.
	sendFn func(event *core.Event, workerID int) bool
}

// New creates a new Publisher instance and starts worker goroutines.
// If apiBaseURL is empty, the default AgentCat API URL is used.
func New(redactFn core.RedactFunc, apiBaseURL string, exporterConfigs map[string]core.ExporterConfig) *Publisher {
	logger := logging.New()

	baseURL := DefaultAPIBaseURL
	if apiBaseURL != "" {
		baseURL = apiBaseURL
	}

	cfg := agentcatapi.NewConfiguration()
	cfg.Servers = agentcatapi.ServerConfigurations{
		{
			URL:         baseURL,
			Description: "AgentCat API",
		},
	}

	apiClient := agentcatapi.NewAPIClient(cfg)

	p := &Publisher{
		queue:      make(chan *core.Event, QueueSize),
		apiClient:  apiClient,
		logger:     logger,
		redactFn:   redactFn,
		shutdownCh: make(chan struct{}),
		maxRetries: MaxRetries,
	}
	p.sendFn = p.sendEvent

	if len(exporterConfigs) > 0 {
		p.telemetry.Store(exporters.NewManager(exporterConfigs))
	}

	for i := range MaxWorkers {
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
			p.safePublishEvent(event, id)
		}
	}

	p.logger.Debugf("Worker %d stopped", id)
}

// safePublishEvent runs publishEvent with top-level panic recovery. A panic
// while publishing any single event is logged and the event dropped, so a
// worker goroutine can never crash the customer's process.
func (p *Publisher) safePublishEvent(event *core.Event, id int) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Errorf("Worker %d recovered from panic while publishing event %s: %v - event dropped",
				id, event.GetId(), r)
		}
	}()
	p.publishEvent(event, id)
}

// SetTelemetryManager replaces the telemetry exporter manager. Passing nil
// disables exporter fan-out.
func (p *Publisher) SetTelemetryManager(m *exporters.Manager) {
	p.telemetry.Store(m)
}

// publishEvent runs the event pipeline (redact -> sanitize -> truncate), fans
// the event out to any configured telemetry exporters, and sends the event to
// the AgentCat API, retrying failed sends with exponential backoff (1s, 2s,
// 4s). Retries are interrupted by shutdown. Events without a project ID
// (telemetry-only mode) go to exporters only and skip the API send.
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

	// Sanitize and truncate fail open: a panic in either stage is logged and
	// the event is published with the stages that succeeded so far.
	p.runStage("sanitization", event, workerID, func() { sanitization.SanitizeEvent(event) })
	p.runStage("truncation", event, workerID, func() { truncation.TruncateEvent(event) })

	// Fan out to telemetry exporters (fire-and-forget, in parallel with and
	// independent of the AgentCat API send; exporter errors are logged, never
	// propagated).
	if tm := p.telemetry.Load(); tm != nil {
		tm.ExportAsync(event)
	}

	// Telemetry-only mode: without a project ID the event is not sent to the
	// AgentCat API.
	if event.ProjectId == "" {
		p.logger.Debugf("Worker %d skipping API send for event %s (telemetry-only mode)",
			workerID, event.GetId())
		return
	}

	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := RetryBaseDelay << (attempt - 1) // 1s, 2s, 4s
			select {
			case <-time.After(backoff):
			case <-p.shutdownCh:
				p.logger.Warnf("Worker %d abandoning retries for event %s: shutting down",
					workerID, event.GetId())
				return
			}
		}

		if p.sendFn(event, workerID) {
			return
		}

		if attempt < p.maxRetries {
			p.logger.Warnf("Worker %d failed to publish event %s (attempt %d/%d), retrying...",
				workerID, event.GetId(), attempt+1, p.maxRetries+1)
		}
	}

	p.logger.Errorf("Worker %d giving up on event %s after %d attempts",
		workerID, event.GetId(), p.maxRetries+1)
}

// runStage runs a pipeline stage with panic recovery so a bug in event
// processing can never break the publishing loop.
func (p *Publisher) runStage(name string, event *core.Event, workerID int, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Warnf("Worker %d %s panicked for event %s: %v - continuing",
				workerID, name, event.GetId(), r)
		}
	}()
	fn()
}

// sendEvent performs a single publish attempt with a per-attempt timeout.
// It returns true on success.
func (p *Publisher) sendEvent(event *core.Event, workerID int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), RequestTimeout)
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
		return false
	}

	if resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		p.logger.Debugf("Worker %d successfully published event %s", workerID, event.GetId())
	} else if resp != nil {
		p.logger.Warnf("Worker %d received unexpected status code: %d", workerID, resp.StatusCode)
	}
	return true
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

		// Signal shutdown so workers waiting in retry backoff abort
		// immediately instead of blocking the drain.
		close(p.shutdownCh)

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

		// Give in-flight telemetry exports a chance to finish within the
		// same shutdown deadline.
		if tm := p.telemetry.Load(); tm != nil {
			tm.Wait(ctx)
		}
	})
	return shutdownErr
}
