package exporters

import (
	"context"
	"sync"

	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
)

// Manager fans events out to a set of configured telemetry exporters.
// Exporter failures are logged and isolated: one failing exporter never
// affects the others or the AgentCat API send.
type Manager struct {
	exporters map[string]core.Exporter
	logger    *logging.Logger
	wg        sync.WaitGroup
}

// NewManager builds a Manager from exporter configurations. Invalid
// configurations and unknown exporter types are skipped with a warning
// logged, mirroring the TypeScript SDK's TelemetryManager.
func NewManager(configs map[string]core.ExporterConfig) *Manager {
	m := &Manager{
		exporters: make(map[string]core.Exporter),
		logger:    logging.New(),
	}

	for name, cfg := range configs {
		exporter, err := m.createExporter(cfg)
		if err != nil {
			m.logger.Warnf("Failed to initialize exporter %s: %v", name, err)
			continue
		}
		if exporter == nil {
			continue
		}
		m.exporters[name] = exporter
		m.logger.Infof("Initialized telemetry exporter: %s", name)
	}

	return m
}

// createExporter constructs a single exporter from its config. It returns
// (nil, nil) for unknown types, which are skipped after a warning.
func (m *Manager) createExporter(cfg core.ExporterConfig) (core.Exporter, error) {
	switch cfg.Type {
	case "otlp":
		return NewOTLPExporter(cfg, m.logger)
	case "datadog":
		return NewDatadogExporter(cfg, m.logger)
	case "sentry":
		return NewSentryExporter(cfg, m.logger)
	case "posthog":
		return NewPostHogExporter(cfg, m.logger)
	default:
		m.logger.Warnf("Unknown exporter type: %s", cfg.Type)
		return nil, nil
	}
}

// ExportAsync fans the event out to every exporter, each in its own
// goroutine, and returns immediately. Errors (and panics) are logged and
// never propagated.
func (m *Manager) ExportAsync(event *core.Event) {
	if event == nil || len(m.exporters) == 0 {
		return
	}

	for name, exporter := range m.exporters {
		m.wg.Add(1)
		go func(name string, exporter core.Exporter) {
			defer m.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					m.logger.Warnf("Telemetry exporter %s panicked: %v", name, r)
				}
			}()
			if err := exporter.Export(event); err != nil {
				m.logger.Warnf("Telemetry export failed for %s: %v", name, err)
			}
		}(name, exporter)
	}
}

// Wait blocks until all in-flight exports complete or the context is done.
func (m *Manager) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		// A worker calling ExportAsync while Wait is in flight (shutdown
		// deadline expired with events still draining) can trip WaitGroup's
		// misuse detection; recover so it can never crash the process. The
		// ctx deadline still bounds the caller.
		defer func() { _ = recover() }()
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Count returns the number of successfully initialized exporters.
func (m *Manager) Count() int {
	return len(m.exporters)
}
