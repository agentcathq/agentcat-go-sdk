package exporters

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
)

// OTLPExporter sends events as OTLP/HTTP JSON trace spans to an
// OpenTelemetry-compatible collector.
type OTLPExporter struct {
	endpoint string
	headers  map[string]string
	logger   *logging.Logger
}

// NewOTLPExporter builds an OTLP exporter. Endpoint is required; the
// /v1/traces path is appended when missing, per the OTLP spec.
func NewOTLPExporter(cfg core.ExporterConfig, logger *logging.Logger) (*OTLPExporter, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("otlp exporter requires an endpoint")
	}

	url := strings.TrimRight(cfg.Endpoint, "/")
	if !strings.HasSuffix(url, "/v1/traces") {
		url += "/v1/traces"
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	for k, v := range cfg.Headers {
		headers[k] = v
	}

	return &OTLPExporter{
		endpoint: url,
		headers:  headers,
		logger:   logger,
	}, nil
}

type otlpAttrValue struct {
	StringValue string `json:"stringValue"`
}

type otlpAttribute struct {
	Key   string        `json:"key"`
	Value otlpAttrValue `json:"value"`
}

type otlpStatus struct {
	Code int `json:"code"`
}

type otlpSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	Name              string          `json:"name"`
	Kind              int             `json:"kind"`
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []otlpAttribute `json:"attributes"`
	Status            otlpStatus      `json:"status"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpResource struct {
	Attributes []otlpAttribute `json:"attributes"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpRequest struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

// Export converts the event into a single OTLP span and POSTs it to the
// collector.
func (e *OTLPExporter) Export(event *core.Event) error {
	span := e.convertToSpan(event)

	req := otlpRequest{
		ResourceSpans: []otlpResourceSpans{
			{
				Resource: otlpResource{
					Attributes: []otlpAttribute{
						{Key: "service.name", Value: otlpAttrValue{StringValue: orDefault(event.GetServerName(), "mcp-server")}},
						{Key: "service.version", Value: otlpAttrValue{StringValue: orDefault(event.GetServerVersion(), "unknown")}},
					},
				},
				ScopeSpans: []otlpScopeSpans{
					{
						Scope: otlpScope{
							Name:    "agentcat",
							Version: orDefault(event.GetAgentcatVersion(), "unknown"),
						},
						Spans: []otlpSpan{span},
					},
				},
			},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("otlp export error: %w", err)
	}

	if err := doPost(e.endpoint, e.headers, bytes.NewReader(body)); err != nil {
		return fmt.Errorf("otlp export error: %w", err)
	}

	e.logger.Debugf("Successfully exported event to OTLP: %s", event.GetId())
	return nil
}

// convertToSpan maps an AgentCat event onto an OTLP span, mirroring the
// TypeScript SDK's convertToOTLPSpan.
func (e *OTLPExporter) convertToSpan(event *core.Event) otlpSpan {
	startNanos := eventTimestampMs(event) * int64(time.Millisecond)
	endNanos := startNanos
	if event.Duration != nil {
		endNanos = startNanos + int64(*event.Duration)*int64(time.Millisecond)
	}

	attrs := []otlpAttribute{
		{Key: "source", Value: otlpAttrValue{StringValue: sourceValue}},
		{Key: "mcp.event_type", Value: otlpAttrValue{StringValue: event.GetEventType()}},
		{Key: "mcp.session_id", Value: otlpAttrValue{StringValue: event.GetSessionId()}},
		{Key: "mcp.project_id", Value: otlpAttrValue{StringValue: event.GetProjectId()}},
		{Key: "mcp.resource_name", Value: otlpAttrValue{StringValue: event.GetResourceName()}},
		{Key: "mcp.user_intent", Value: otlpAttrValue{StringValue: event.GetUserIntent()}},
		{Key: "mcp.actor_id", Value: otlpAttrValue{StringValue: event.GetIdentifyActorGivenId()}},
		{Key: "mcp.actor_name", Value: otlpAttrValue{StringValue: event.GetIdentifyActorName()}},
		{Key: "mcp.client_name", Value: otlpAttrValue{StringValue: event.GetClientName()}},
		{Key: "mcp.client_version", Value: otlpAttrValue{StringValue: event.GetClientVersion()}},
	}

	// Customer-defined tags as individual namespaced attributes (sorted for
	// deterministic output).
	tags := event.GetTags()
	for _, k := range sortedKeys(tags) {
		attrs = append(attrs, otlpAttribute{
			Key:   "agentcat.tag." + k,
			Value: otlpAttrValue{StringValue: tags[k]},
		})
	}

	// Customer-defined properties JSON-encoded in a single attribute.
	if props := event.GetProperties(); len(props) > 0 {
		if encoded, err := json.Marshal(props); err == nil {
			attrs = append(attrs, otlpAttribute{
				Key:   "agentcat.properties",
				Value: otlpAttrValue{StringValue: string(encoded)},
			})
		}
	}

	// Remove empty attributes, matching the TS exporter's filter.
	filtered := attrs[:0]
	for _, a := range attrs {
		if a.Value.StringValue != "" {
			filtered = append(filtered, a)
		}
	}

	statusCode := 1 // OK
	if event.GetIsError() {
		statusCode = 2 // ERROR
	}

	return otlpSpan{
		TraceID:           TraceID(event.GetSessionId()),
		SpanID:            SpanID(event.GetId()),
		Name:              orDefault(event.GetEventType(), "mcp.event"),
		Kind:              2, // SPAN_KIND_SERVER
		StartTimeUnixNano: strconv.FormatInt(startNanos, 10),
		EndTimeUnixNano:   strconv.FormatInt(endNanos, 10),
		Attributes:        filtered,
		Status:            otlpStatus{Code: statusCode},
	}
}
