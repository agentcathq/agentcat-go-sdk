package exporters

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/segmentio/ksuid"
	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
)

// defaultPostHogHost is the default PostHog ingestion host (US region).
const defaultPostHogHost = "https://us.i.posthog.com"

// mcpToolsCallEventType is the wire event type for tool calls.
const mcpToolsCallEventType = "mcp:tools/call"

// PostHogExporter sends events to PostHog's batch capture API.
type PostHogExporter struct {
	batchURL        string
	apiKey          string
	enableAITracing bool
	logger          *logging.Logger
}

// NewPostHogExporter builds a PostHog exporter. APIKey is required; Host
// defaults to the US cloud instance; EnableAITracing (default false) emits
// $ai_span events for tool calls alongside regular capture events.
func NewPostHogExporter(cfg core.ExporterConfig, logger *logging.Logger) (*PostHogExporter, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("posthog exporter requires an apiKey")
	}

	host := strings.TrimRight(orDefault(cfg.Host, defaultPostHogHost), "/")

	e := &PostHogExporter{
		batchURL:        host + "/batch",
		apiKey:          cfg.APIKey,
		enableAITracing: cfg.EnableAITracing,
		logger:          logger,
	}

	logger.Debugf("PostHogExporter: initialized with endpoint %s", e.batchURL)
	return e, nil
}

var idPrefixPattern = regexp.MustCompile(`^[a-z]+_`)

// ToUUIDv7 generates a deterministic UUIDv7 from a prefixed KSUID (e.g.
// ses_xxx). It uses the KSUID's embedded timestamp for the UUIDv7 timestamp
// portion and a SHA-256 hash of the full ID for the random bits, matching the
// TypeScript SDK's toUUIDv7 exactly.
func ToUUIDv7(prefixedID string) string {
	ksuidStr := idPrefixPattern.ReplaceAllString(prefixedID, "")

	var timestampMs int64
	if parsed, err := ksuid.Parse(ksuidStr); err == nil {
		timestampMs = parsed.Time().UnixMilli()
	} else {
		// Fallback: if KSUID parsing fails, use current time.
		timestampMs = time.Now().UnixMilli()
	}

	// Hash the full ID for deterministic random bits.
	hash := sha256.Sum256([]byte(prefixedID))

	var buf [16]byte

	// Bytes 0-5: 48-bit Unix timestamp in milliseconds (big-endian).
	buf[0] = byte(timestampMs >> 40)
	buf[1] = byte(timestampMs >> 32)
	buf[2] = byte(timestampMs >> 24)
	buf[3] = byte(timestampMs >> 16)
	buf[4] = byte(timestampMs >> 8)
	buf[5] = byte(timestampMs)

	// Byte 6: version 7 (0111) + high 4 bits of rand_a from hash.
	buf[6] = 0x70 | (hash[0] & 0x0f)
	// Byte 7: low 8 bits of rand_a from hash.
	buf[7] = hash[1]

	// Byte 8: variant 10 + high 6 bits of rand_b from hash.
	buf[8] = 0x80 | (hash[2] & 0x3f)
	// Bytes 9-15: remaining rand_b from hash.
	copy(buf[9:], hash[3:10])

	h := hex.EncodeToString(buf[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

func distinctID(event *core.Event) string {
	if id := event.GetIdentifyActorGivenId(); id != "" {
		return id
	}
	if id := event.GetSessionId(); id != "" {
		return id
	}
	return "anonymous"
}

// posthogISOFormat matches JavaScript's Date.toISOString() millisecond format.
const posthogISOFormat = "2006-01-02T15:04:05.000Z07:00"

func posthogTimestamp(event *core.Event) string {
	if event.Timestamp != nil {
		return event.Timestamp.UTC().Format(posthogISOFormat)
	}
	return time.Now().UTC().Format(posthogISOFormat)
}

type posthogCaptureEvent struct {
	Event      string         `json:"event"`
	DistinctID string         `json:"distinct_id"`
	Properties map[string]any `json:"properties"`
	Timestamp  string         `json:"timestamp"`
	Type       string         `json:"type"`
}

// Export sends the event (plus an optional $exception and $ai_span) to
// PostHog as a single batch.
func (e *PostHogExporter) Export(event *core.Event) error {
	batch := []posthogCaptureEvent{e.buildCaptureEvent(event)}

	// Send an $exception event alongside if this is an error.
	if event.GetIsError() && event.Error != nil {
		batch = append(batch, e.buildExceptionEvent(event))
	}

	// Send an $ai_span for tool calls when AI tracing is enabled.
	if e.enableAITracing && event.GetEventType() == mcpToolsCallEventType {
		batch = append(batch, e.buildAISpanEvent(event))
	}

	e.logger.Debugf("PostHogExporter: sending %d event(s) for %s", len(batch), event.GetId())

	body, err := json.Marshal(map[string]any{
		"api_key": e.apiKey,
		"batch":   batch,
	})
	if err != nil {
		return fmt.Errorf("posthog export error: %w", err)
	}

	resp, err := doPost(e.batchURL, map[string]string{"Content-Type": "application/json"}, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("posthog export error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("posthog export failed: %s", resp.Status)
	}

	return nil
}

func (e *PostHogExporter) buildCaptureEvent(event *core.Event) posthogCaptureEvent {
	properties := map[string]any{
		"$session_id": ToUUIDv7(event.GetSessionId()),
		"source":      sourceValue,
	}

	if rn := event.GetResourceName(); rn != "" {
		properties["resource_name"] = rn
		if event.GetEventType() == mcpToolsCallEventType {
			properties["tool_name"] = rn
		}
	}
	if event.Duration != nil {
		properties["duration_ms"] = *event.Duration
	}
	setIfNotEmpty := func(key, value string) {
		if value != "" {
			properties[key] = value
		}
	}
	setIfNotEmpty("server_name", event.GetServerName())
	setIfNotEmpty("server_version", event.GetServerVersion())
	setIfNotEmpty("client_name", event.GetClientName())
	setIfNotEmpty("client_version", event.GetClientVersion())
	setIfNotEmpty("project_id", event.GetProjectId())
	setIfNotEmpty("user_intent", event.GetUserIntent())
	if event.IsError != nil {
		properties["is_error"] = *event.IsError
	}

	if event.Parameters != nil {
		properties["parameters"] = event.Parameters
	}
	if event.Response != nil {
		properties["response"] = event.Response
	}

	// Set person properties from identity data.
	set := make(map[string]any)
	if name := event.GetIdentifyActorName(); name != "" {
		set["name"] = name
	}
	for k, v := range event.GetIdentifyData() {
		set[k] = v
	}
	if len(set) > 0 {
		properties["$set"] = set
	}

	// Spread customer-defined tags directly (can override AgentCat defaults).
	for k, v := range event.GetTags() {
		properties[k] = v
	}
	// Spread customer-defined properties directly (can override AgentCat defaults).
	for k, v := range event.GetProperties() {
		properties[k] = v
	}

	return posthogCaptureEvent{
		Event:      mapEventType(event.GetEventType()),
		DistinctID: distinctID(event),
		Properties: properties,
		Timestamp:  posthogTimestamp(event),
		Type:       "capture",
	}
}

func (e *PostHogExporter) buildExceptionEvent(event *core.Event) posthogCaptureEvent {
	properties := map[string]any{
		"$exception_source": "backend",
		"$session_id":       ToUUIDv7(event.GetSessionId()),
	}

	if event.Error != nil {
		if msg, ok := event.Error["message"].(string); ok && msg != "" {
			properties["$exception_message"] = msg
		}
		if typ, ok := event.Error["type"].(string); ok && typ != "" {
			properties["$exception_type"] = typ
		}
		if stack, ok := event.Error["stack"].(string); ok && stack != "" {
			properties["$exception_stacktrace"] = stack
		}
	}

	// Add tool/resource context.
	if rn := event.GetResourceName(); rn != "" {
		properties["resource_name"] = rn
		if event.GetEventType() == mcpToolsCallEventType {
			properties["tool_name"] = rn
		}
	}
	setIfNotEmpty := func(key, value string) {
		if value != "" {
			properties[key] = value
		}
	}
	setIfNotEmpty("server_name", event.GetServerName())
	setIfNotEmpty("server_version", event.GetServerVersion())
	setIfNotEmpty("client_name", event.GetClientName())
	setIfNotEmpty("client_version", event.GetClientVersion())

	return posthogCaptureEvent{
		Event:      "$exception",
		DistinctID: distinctID(event),
		Properties: properties,
		Timestamp:  posthogTimestamp(event),
		Type:       "capture",
	}
}

func (e *PostHogExporter) buildAISpanEvent(event *core.Event) posthogCaptureEvent {
	properties := map[string]any{
		"$ai_session_id": "agentcat_" + event.GetSessionId(),
		"$ai_trace_id":   ToUUIDv7(event.GetSessionId()),
		"$ai_span_id":    ToUUIDv7(event.GetId()),
		"$ai_span_name":  orDefault(event.GetResourceName(), "unknown_tool"),
		"$ai_is_error":   event.GetIsError(),
		"$session_id":    ToUUIDv7(event.GetSessionId()),
		"source":         sourceValue,
	}

	if event.Duration != nil {
		properties["$ai_latency"] = float64(*event.Duration) / 1000
	}
	if event.GetIsError() && event.Error != nil {
		properties["$ai_error"] = event.Error
	}
	if event.Parameters != nil {
		properties["$ai_input_state"] = event.Parameters
	}
	if event.Response != nil {
		properties["$ai_output_state"] = event.Response
	}
	if sn := event.GetServerName(); sn != "" {
		properties["server_name"] = sn
	}
	if cn := event.GetClientName(); cn != "" {
		properties["client_name"] = cn
	}

	// Spread customer tags/properties directly (can override AgentCat
	// defaults, including reserved $ai_* fields).
	for k, v := range event.GetTags() {
		properties[k] = v
	}
	for k, v := range event.GetProperties() {
		properties[k] = v
	}

	return posthogCaptureEvent{
		Event:      "$ai_span",
		DistinctID: distinctID(event),
		Properties: properties,
		Timestamp:  posthogTimestamp(event),
		Type:       "capture",
	}
}

// mapEventType maps AgentCat wire event types to PostHog event names.
func mapEventType(eventType string) string {
	switch eventType {
	case "mcp:tools/call":
		return "mcp_tool_call"
	case "mcp:tools/list":
		return "mcp_tools_list"
	case "mcp:initialize":
		return "mcp_initialize"
	case "mcp:resources/read":
		return "mcp_resource_read"
	case "mcp:resources/list":
		return "mcp_resources_list"
	case "mcp:prompts/get":
		return "mcp_prompt_get"
	case "mcp:prompts/list":
		return "mcp_prompts_list"
	default:
		name := strings.TrimPrefix(eventType, "mcp:")
		return "mcp_" + strings.ReplaceAll(name, "/", "_")
	}
}
