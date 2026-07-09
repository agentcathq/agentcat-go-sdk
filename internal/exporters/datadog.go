package exporters

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
)

// DatadogExporter sends events to Datadog as logs (Logs API v2) and metrics
// (Metrics API v1).
type DatadogExporter struct {
	logsURL    string
	metricsURL string
	apiKey     string
	service    string
	env        string
	logger     *logging.Logger
}

// NewDatadogExporter builds a Datadog exporter. APIKey, Site, and Service are
// required; Env is an optional environment tag.
func NewDatadogExporter(cfg core.ExporterConfig, logger *logging.Logger) (*DatadogExporter, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("datadog exporter requires an apiKey")
	}
	if cfg.Site == "" {
		return nil, errors.New("datadog exporter requires a site (e.g. datadoghq.com)")
	}
	if cfg.Service == "" {
		return nil, errors.New("datadog exporter requires a service name")
	}

	site := cfg.Site
	site = strings.TrimPrefix(site, "https://")
	site = strings.TrimPrefix(site, "http://")
	site = strings.TrimRight(site, "/")

	return &DatadogExporter{
		logsURL:    fmt.Sprintf("https://http-intake.logs.%s/api/v2/logs", site),
		metricsURL: fmt.Sprintf("https://api.%s/api/v1/series", site),
		apiKey:     cfg.APIKey,
		service:    cfg.Service,
		env:        cfg.Env,
		logger:     logger,
	}, nil
}

type datadogTraceInfo struct {
	TraceID string `json:"trace_id"`
	SpanID  string `json:"span_id"`
}

type datadogError struct {
	Message string `json:"message"`
}

type datadogLog struct {
	Message   string            `json:"message"`
	Service   string            `json:"service"`
	DDSource  string            `json:"ddsource"`
	DDTags    string            `json:"ddtags"`
	Timestamp int64             `json:"timestamp"`
	Status    string            `json:"status,omitempty"`
	DD        *datadogTraceInfo `json:"dd,omitempty"`
	Error     *datadogError     `json:"error,omitempty"`
	MCP       map[string]any    `json:"mcp"`
}

type datadogMetric struct {
	Metric string       `json:"metric"`
	Type   string       `json:"type"`
	Points [][2]float64 `json:"points"`
	Tags   []string     `json:"tags,omitempty"`
}

var datadogTagKeySanitizer = regexp.MustCompile(`[\s:,]+`)

// Export sends the event to Datadog as one log entry plus event/duration/
// error metric series. Both sends are attempted; failures are joined.
func (e *DatadogExporter) Export(event *core.Event) error {
	logEntry := e.eventToLog(event)
	metrics := e.eventToMetrics(event)

	e.logger.Debugf("DatadogExporter: sending event %s (%d metric series)",
		event.GetId(), len(metrics))

	headers := map[string]string{
		"DD-API-KEY":   e.apiKey,
		"Content-Type": "application/json",
	}

	var errs []error

	if body, err := json.Marshal([]datadogLog{logEntry}); err != nil {
		errs = append(errs, fmt.Errorf("datadog logs marshal: %w", err))
	} else if err := doPost(e.logsURL, headers, bytes.NewReader(body)); err != nil {
		errs = append(errs, fmt.Errorf("datadog logs: %w", err))
	}

	if body, err := json.Marshal(map[string]any{"series": metrics}); err != nil {
		errs = append(errs, fmt.Errorf("datadog metrics marshal: %w", err))
	} else if err := doPost(e.metricsURL, headers, bytes.NewReader(body)); err != nil {
		errs = append(errs, fmt.Errorf("datadog metrics: %w", err))
	}

	return errors.Join(errs...)
}

func (e *DatadogExporter) eventToLog(event *core.Event) datadogLog {
	var tags []string

	if e.env != "" {
		tags = append(tags, "env:"+e.env)
	}
	if et := event.GetEventType(); et != "" {
		tags = append(tags, "event_type:"+strings.ReplaceAll(et, "/", "."))
	}
	if rn := event.GetResourceName(); rn != "" {
		tags = append(tags, "resource:"+rn)
	}
	if event.GetIsError() {
		tags = append(tags, "error:true")
	}
	tags = append(tags, "source:"+sourceValue)

	// Customer-defined tags, namespaced to avoid collisions with reserved
	// Datadog tags (sorted for deterministic output).
	customerTags := event.GetTags()
	for _, k := range sortedKeys(customerTags) {
		key := datadogTagKeySanitizer.ReplaceAllString(strings.ToLower(k), "_")
		value := strings.ReplaceAll(customerTags[k], ",", "_")
		tags = append(tags, fmt.Sprintf("agentcat.%s:%s", key, value))
	}

	mcp := map[string]any{}
	setIfNotEmpty(mcp, "session_id", event.GetSessionId())
	setIfNotEmpty(mcp, "event_id", event.GetId())
	setIfNotEmpty(mcp, "event_type", event.GetEventType())
	setIfNotEmpty(mcp, "resource", event.GetResourceName())
	setIfNotEmpty(mcp, "user_intent", event.GetUserIntent())
	setIfNotEmpty(mcp, "actor_id", event.GetIdentifyActorGivenId())
	setIfNotEmpty(mcp, "actor_name", event.GetIdentifyActorName())
	setIfNotEmpty(mcp, "client_name", event.GetClientName())
	setIfNotEmpty(mcp, "client_version", event.GetClientVersion())
	setIfNotEmpty(mcp, "server_name", event.GetServerName())
	setIfNotEmpty(mcp, "server_version", event.GetServerVersion())
	if event.Duration != nil {
		mcp["duration_ms"] = *event.Duration
	}
	if event.IsError != nil {
		mcp["is_error"] = *event.IsError
	}
	if event.Error != nil {
		mcp["error"] = event.Error
	}
	if len(customerTags) > 0 {
		mcp["tags"] = customerTags
	}
	if props := event.GetProperties(); len(props) > 0 {
		mcp["properties"] = props
	}

	status := "info"
	if event.GetIsError() {
		status = "error"
	}

	logEntry := datadogLog{
		Message: fmt.Sprintf("%s - %s",
			orDefault(event.GetEventType(), "unknown"),
			orDefault(event.GetResourceName(), "unknown")),
		Service:   e.service,
		DDSource:  sourceValue,
		DDTags:    strings.Join(tags, ","),
		Timestamp: eventTimestampMs(event),
		Status:    status,
		DD: &datadogTraceInfo{
			TraceID: DatadogTraceID(event.GetSessionId()),
			SpanID:  DatadogSpanID(event.GetId()),
		},
		MCP: mcp,
	}

	// Root-level error for Datadog error tracking. Events always carry error
	// details as a structured map, so it is JSON-encoded like the TypeScript
	// SDK's JSON.stringify fallback.
	if event.GetIsError() && event.Error != nil {
		message := ""
		if encoded, err := json.Marshal(event.Error); err == nil {
			message = string(encoded)
		}
		logEntry.Error = &datadogError{Message: message}
	}

	return logEntry
}

func (e *DatadogExporter) eventToMetrics(event *core.Event) []datadogMetric {
	timestamp := float64(eventTimestampMs(event) / 1000)

	tags := []string{"service:" + e.service}
	if e.env != "" {
		tags = append(tags, "env:"+e.env)
	}
	if et := event.GetEventType(); et != "" {
		tags = append(tags, "event_type:"+strings.ReplaceAll(et, "/", "."))
	}
	if rn := event.GetResourceName(); rn != "" {
		tags = append(tags, "resource:"+rn)
	}

	metrics := []datadogMetric{
		{
			Metric: "mcp.events.count",
			Type:   "count",
			Points: [][2]float64{{timestamp, 1}},
			Tags:   tags,
		},
	}

	if event.Duration != nil && *event.Duration != 0 {
		metrics = append(metrics, datadogMetric{
			Metric: "mcp.event.duration",
			Type:   "gauge",
			Points: [][2]float64{{timestamp, float64(*event.Duration)}},
			Tags:   tags,
		})
	}

	if event.GetIsError() {
		metrics = append(metrics, datadogMetric{
			Metric: "mcp.errors.count",
			Type:   "count",
			Points: [][2]float64{{timestamp, 1}},
			Tags:   tags,
		})
	}

	return metrics
}
