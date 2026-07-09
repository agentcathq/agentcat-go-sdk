package exporters

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
)

// SentryExporter sends events to Sentry via the envelope API. Every event is
// sent as a Sentry Log; error events additionally create Sentry Issues; and
// transactions are sent when tracing is enabled.
type SentryExporter struct {
	endpoint      string
	authHeader    string
	environment   string
	release       string
	enableTracing bool
	logger        *logging.Logger
}

// parsedDSN holds the components of a Sentry DSN
// (protocol://publicKey@host[:port]/path/projectId).
type parsedDSN struct {
	protocol  string
	publicKey string
	host      string
	port      string
	path      string
	projectID string
}

var dsnPattern = regexp.MustCompile(`^(https?)://([a-f0-9]+)@([\w.-]+)(:\d+)?(/.*)?/(\d+)$`)

// NewSentryExporter builds a Sentry exporter. DSN is required; Environment
// and Release are optional tags; EnableTracing (default false) additionally
// sends transactions for performance monitoring.
func NewSentryExporter(cfg core.ExporterConfig, logger *logging.Logger) (*SentryExporter, error) {
	if cfg.DSN == "" {
		return nil, errors.New("sentry exporter requires a dsn")
	}

	dsn, err := parseDSN(cfg.DSN)
	if err != nil {
		return nil, err
	}

	port := ""
	if dsn.port != "" {
		port = ":" + dsn.port
	}
	endpoint := fmt.Sprintf("%s://%s%s%s/api/%s/envelope/",
		dsn.protocol, dsn.host, port, dsn.path, dsn.projectID)

	e := &SentryExporter{
		endpoint: endpoint,
		authHeader: fmt.Sprintf(
			"Sentry sentry_version=7, sentry_client=agentcat/1.0.0, sentry_key=%s",
			dsn.publicKey),
		environment:   cfg.Environment,
		release:       cfg.Release,
		enableTracing: cfg.EnableTracing,
		logger:        logger,
	}

	logger.Debugf("SentryExporter: initialized with endpoint %s", endpoint)
	return e, nil
}

func parseDSN(dsn string) (parsedDSN, error) {
	m := dsnPattern.FindStringSubmatch(dsn)
	if m == nil {
		return parsedDSN{}, fmt.Errorf("invalid Sentry DSN: %s", dsn)
	}
	return parsedDSN{
		protocol:  m[1],
		publicKey: m[2],
		host:      m[3],
		port:      strings.TrimPrefix(m[4], ":"),
		path:      m[5],
		projectID: m[6],
	}, nil
}

type sentryLogAttribute struct {
	Value any    `json:"value"`
	Type  string `json:"type"`
}

type sentryLog struct {
	Timestamp  float64                       `json:"timestamp"`
	TraceID    string                        `json:"trace_id"`
	EventID    string                        `json:"event_id"`
	Level      string                        `json:"level"`
	Body       string                        `json:"body"`
	Attributes map[string]sentryLogAttribute `json:"attributes,omitempty"`
}

type sentryTraceContext struct {
	TraceID      string `json:"trace_id"`
	SpanID       string `json:"span_id"`
	ParentSpanID string `json:"parent_span_id,omitempty"`
	Op           string `json:"op,omitempty"`
	Status       string `json:"status,omitempty"`
}

type sentryTransaction struct {
	Type           string            `json:"type"`
	EventID        string            `json:"event_id"`
	Timestamp      float64           `json:"timestamp"`
	StartTimestamp float64           `json:"start_timestamp"`
	Transaction    string            `json:"transaction"`
	Contexts       map[string]any    `json:"contexts"`
	Tags           map[string]string `json:"tags,omitempty"`
	Extra          map[string]any    `json:"extra,omitempty"`
}

type sentryExceptionMechanism struct {
	Type    string `json:"type"`
	Handled bool   `json:"handled"`
}

type sentryExceptionValue struct {
	Type      string                    `json:"type"`
	Value     string                    `json:"value"`
	Mechanism *sentryExceptionMechanism `json:"mechanism,omitempty"`
}

type sentryException struct {
	Values []sentryExceptionValue `json:"values"`
}

type sentryErrorEvent struct {
	Type        string            `json:"type"`
	EventID     string            `json:"event_id"`
	Timestamp   float64           `json:"timestamp"`
	Level       string            `json:"level"`
	Exception   sentryException   `json:"exception"`
	Contexts    map[string]any    `json:"contexts,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Extra       map[string]any    `json:"extra,omitempty"`
	Transaction string            `json:"transaction,omitempty"`
}

// Export sends a log envelope for every event, an optional transaction
// envelope when tracing is enabled, and an error-event envelope for Issue
// creation when the event is an error.
func (e *SentryExporter) Export(event *core.Event) error {
	var errs []error

	// Compute the deterministic trace/span IDs once; every envelope uses them.
	traceID := TraceID(event.GetSessionId())
	spanID := SpanID(event.GetId())

	// ALWAYS send a log.
	logItem := e.eventToLog(event, traceID, spanID)
	envelope, err := e.createLogEnvelope(logItem)
	if err != nil {
		errs = append(errs, fmt.Errorf("sentry log envelope: %w", err))
	} else if err := e.sendEnvelope(envelope); err != nil {
		errs = append(errs, fmt.Errorf("sentry log export: %w", err))
	}

	// OPTIONALLY send a transaction for performance monitoring.
	var transaction *sentryTransaction
	if e.enableTracing {
		tx := e.eventToTransaction(event, traceID, spanID)
		transaction = &tx
		envelope, err := e.createTransactionEnvelope(tx)
		if err != nil {
			errs = append(errs, fmt.Errorf("sentry transaction envelope: %w", err))
		} else if err := e.sendEnvelope(envelope); err != nil {
			errs = append(errs, fmt.Errorf("sentry transaction export: %w", err))
		}
	}

	// ALWAYS send an error event for Issue creation when this is an error.
	if event.GetIsError() {
		errorEvent := e.eventToErrorEvent(event, transaction, traceID, spanID)
		envelope, err := e.createErrorEnvelope(errorEvent)
		if err != nil {
			errs = append(errs, fmt.Errorf("sentry error envelope: %w", err))
		} else if err := e.sendEnvelope(envelope); err != nil {
			errs = append(errs, fmt.Errorf("sentry error export: %w", err))
		}
	}

	return errors.Join(errs...)
}

func (e *SentryExporter) sendEnvelope(envelope string) error {
	headers := map[string]string{
		"X-Sentry-Auth": e.authHeader,
		"Content-Type":  "application/x-sentry-envelope",
	}
	return doPost(e.endpoint, headers, strings.NewReader(envelope))
}

func eventTimestampSeconds(event *core.Event) float64 {
	return float64(eventTimestampMs(event)) / 1000
}

func (e *SentryExporter) eventToLog(event *core.Event, traceID, spanID string) sentryLog {
	level := "info"
	if event.GetIsError() {
		level = "error"
	}

	message := "MCP " + orDefault(event.GetEventType(), "event")
	if rn := event.GetResourceName(); rn != "" {
		message += ": " + rn
	}

	return sentryLog{
		Timestamp: eventTimestampSeconds(event),
		TraceID:   traceID,
		// Deterministic 32-hex event_id derived from the event ID.
		EventID:    spanID + spanID,
		Level:      level,
		Body:       message,
		Attributes: e.buildLogAttributes(event),
	}
}

func (e *SentryExporter) buildLogAttributes(event *core.Event) map[string]sentryLogAttribute {
	attrs := make(map[string]sentryLogAttribute)
	setString := func(key, value string) {
		if value != "" {
			attrs[key] = sentryLogAttribute{Value: value, Type: "string"}
		}
	}

	setString("eventType", event.GetEventType())
	setString("resourceName", event.GetResourceName())
	setString("serverName", event.GetServerName())
	setString("clientName", event.GetClientName())
	setString("sessionId", event.GetSessionId())
	setString("projectId", event.GetProjectId())
	if event.Duration != nil {
		attrs["duration_ms"] = sentryLogAttribute{Value: *event.Duration, Type: "double"}
	}
	setString("actorId", event.GetIdentifyActorGivenId())
	setString("actorName", event.GetIdentifyActorName())
	setString("userIntent", event.GetUserIntent())
	setString("serverVersion", event.GetServerVersion())
	setString("clientVersion", event.GetClientVersion())
	if event.IsError != nil {
		attrs["isError"] = sentryLogAttribute{Value: *event.IsError, Type: "boolean"}
	}

	return attrs
}

func (e *SentryExporter) createLogEnvelope(logItem sentryLog) (string, error) {
	envelopeHeader := map[string]any{
		"event_id": logItem.EventID,
		"sent_at":  time.Now().UTC().Format(time.RFC3339),
	}
	itemHeader := map[string]any{
		"type":         "log",
		"item_count":   1,
		"content_type": "application/vnd.sentry.items.log+json",
	}
	payload := map[string]any{
		"items": []sentryLog{logItem},
	}
	return joinEnvelope(true, envelopeHeader, itemHeader, payload)
}

func (e *SentryExporter) eventToTransaction(event *core.Event, traceID, spanID string) sentryTransaction {
	endTimestamp := eventTimestampSeconds(event)
	startTimestamp := endTimestamp
	if event.Duration != nil {
		startTimestamp = endTimestamp - float64(*event.Duration)/1000
	}

	status := "ok"
	if event.GetIsError() {
		status = "internal_error"
	}

	return sentryTransaction{
		Type:           "transaction",
		EventID:        spanID + randomHex(8),
		Timestamp:      endTimestamp,
		StartTimestamp: startTimestamp,
		Transaction:    transactionName(event),
		Contexts: e.buildContexts(event, sentryTraceContext{
			TraceID: traceID,
			SpanID:  spanID,
			Op:      orDefault(event.GetEventType(), "mcp.event"),
			Status:  status,
		}),
		Tags:  e.buildTags(event),
		Extra: e.buildExtra(event),
	}
}

func transactionName(event *core.Event) string {
	if rn := event.GetResourceName(); rn != "" {
		return fmt.Sprintf("%s - %s", orDefault(event.GetEventType(), "mcp"), rn)
	}
	return orDefault(event.GetEventType(), "mcp.event")
}

func (e *SentryExporter) buildTags(event *core.Event) map[string]string {
	tags := map[string]string{
		"source": sourceValue,
	}

	setTagIfNotEmpty(tags, "environment", e.environment)
	setTagIfNotEmpty(tags, "release", e.release)
	setTagIfNotEmpty(tags, "event_type", event.GetEventType())
	setTagIfNotEmpty(tags, "resource", event.GetResourceName())
	setTagIfNotEmpty(tags, "server_name", event.GetServerName())
	setTagIfNotEmpty(tags, "client_name", event.GetClientName())
	setTagIfNotEmpty(tags, "actor_id", event.GetIdentifyActorGivenId())

	// Customer-defined tags, namespaced to avoid collisions with Sentry
	// reserved fields.
	for k, v := range event.GetTags() {
		tags["agentcat."+k] = v
	}

	return tags
}

func (e *SentryExporter) buildExtra(event *core.Event) map[string]any {
	extra := make(map[string]any)
	setIfNotEmpty(extra, "session_id", event.GetSessionId())
	setIfNotEmpty(extra, "project_id", event.GetProjectId())
	setIfNotEmpty(extra, "user_intent", event.GetUserIntent())
	setIfNotEmpty(extra, "actor_name", event.GetIdentifyActorName())
	setIfNotEmpty(extra, "server_version", event.GetServerVersion())
	setIfNotEmpty(extra, "client_version", event.GetClientVersion())
	if event.Duration != nil {
		extra["duration_ms"] = *event.Duration
	}
	if event.Error != nil {
		extra["error"] = event.Error
	}
	return extra
}

func (e *SentryExporter) buildContexts(event *core.Event, traceCtx sentryTraceContext) map[string]any {
	contexts := map[string]any{
		"trace": traceCtx,
	}
	// Customer-defined properties as a custom context.
	if props := event.GetProperties(); len(props) > 0 {
		contexts["agentcat"] = props
	}
	return contexts
}

func (e *SentryExporter) eventToErrorEvent(event *core.Event, transaction *sentryTransaction, traceID, spanID string) sentryErrorEvent {
	errorMessage := "Unknown error"
	errorType := "ToolCallError"

	if event.Error != nil {
		if msg, ok := event.Error["message"].(string); ok {
			errorMessage = msg
		} else if encoded, err := json.Marshal(event.Error); err == nil {
			errorMessage = string(encoded)
		}
		if typ, ok := event.Error["type"].(string); ok {
			errorType = typ
		}
	}

	// Correlate with the transaction's trace context when available.
	traceCtx := sentryTraceContext{
		TraceID: traceID,
		SpanID:  spanID,
		Op:      orDefault(event.GetEventType(), "mcp.event"),
	}
	timestamp := eventTimestampSeconds(event)
	txName := transactionName(event)
	if transaction != nil {
		if tc, ok := transaction.Contexts["trace"].(sentryTraceContext); ok {
			traceCtx.TraceID = tc.TraceID
			traceCtx.ParentSpanID = tc.SpanID
			traceCtx.Op = tc.Op
		}
		timestamp = transaction.Timestamp
		txName = transaction.Transaction
	}

	contexts := e.buildContexts(event, traceCtx)
	mcpCtx := make(map[string]any)
	setIfNotEmpty(mcpCtx, "resource_name", event.GetResourceName())
	setIfNotEmpty(mcpCtx, "session_id", event.GetSessionId())
	setIfNotEmpty(mcpCtx, "event_type", event.GetEventType())
	setIfNotEmpty(mcpCtx, "user_intent", event.GetUserIntent())
	contexts["mcp"] = mcpCtx

	return sentryErrorEvent{
		Type:      "event",
		EventID:   spanID + randomHex(8),
		Timestamp: timestamp,
		Level:     "error",
		Exception: sentryException{
			Values: []sentryExceptionValue{
				{
					Type:  errorType,
					Value: errorMessage,
					Mechanism: &sentryExceptionMechanism{
						Type:    "mcp_tool_call",
						Handled: false,
					},
				},
			},
		},
		Contexts:    contexts,
		Tags:        e.buildTags(event),
		Extra:       e.buildExtra(event),
		Transaction: txName,
	}
}

func (e *SentryExporter) createTransactionEnvelope(tx sentryTransaction) (string, error) {
	envelopeHeader := map[string]any{
		"event_id": tx.EventID,
		"sent_at":  time.Now().UTC().Format(time.RFC3339),
	}
	itemHeader := map[string]any{
		"type": "transaction",
	}
	return joinEnvelope(false, envelopeHeader, itemHeader, tx)
}

func (e *SentryExporter) createErrorEnvelope(errorEvent sentryErrorEvent) (string, error) {
	envelopeHeader := map[string]any{
		"event_id": errorEvent.EventID,
		"sent_at":  time.Now().UTC().Format(time.RFC3339),
	}
	itemHeader := map[string]any{
		"type":         "event",
		"content_type": "application/json",
	}
	return joinEnvelope(false, envelopeHeader, itemHeader, errorEvent)
}

// joinEnvelope serializes envelope sections as newline-separated JSON.
// Log envelopes require a trailing newline.
func joinEnvelope(trailingNewline bool, sections ...any) (string, error) {
	parts := make([]string, 0, len(sections))
	for _, s := range sections {
		encoded, err := json.Marshal(s)
		if err != nil {
			return "", err
		}
		parts = append(parts, string(encoded))
	}
	envelope := strings.Join(parts, "\n")
	if trailingNewline {
		envelope += "\n"
	}
	return envelope, nil
}
