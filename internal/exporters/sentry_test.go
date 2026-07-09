package exporters

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
)

// sentryEnvelope is one received envelope split into its JSON lines.
type sentryEnvelope struct {
	header  map[string]any
	item    map[string]any
	payload map[string]any
}

func parseEnvelope(t *testing.T, raw string) sentryEnvelope {
	t.Helper()
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("envelope has %d lines, want 3: %q", len(lines), raw)
	}
	var env sentryEnvelope
	if err := json.Unmarshal([]byte(lines[0]), &env.header); err != nil {
		t.Fatalf("envelope header: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &env.item); err != nil {
		t.Fatalf("item header: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[2]), &env.payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	return env
}

func newSentryExporterForTest(t *testing.T, enableTracing bool) (*SentryExporter, *[]string, *[]string) {
	t.Helper()

	var bodies []string
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/42/envelope/" {
			t.Errorf("path = %q, want /api/42/envelope/", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-sentry-envelope" {
			t.Errorf("content type = %q", ct)
		}
		auths = append(auths, r.Header.Get("X-Sentry-Auth"))
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	dsn := "http://abcdef0123456789@" + host + "/42"

	e, err := NewSentryExporter(core.ExporterConfig{
		Type:          "sentry",
		DSN:           dsn,
		Environment:   "staging-env",
		Release:       "v1.2.3",
		EnableTracing: enableTracing,
	}, logging.New())
	if err != nil {
		t.Fatal(err)
	}
	return e, &bodies, &auths
}

func TestSentryExporter_RequiresDSN(t *testing.T) {
	if _, err := NewSentryExporter(core.ExporterConfig{Type: "sentry"}, logging.New()); err == nil {
		t.Fatal("expected error for missing DSN")
	}
}

func TestSentryExporter_InvalidDSN(t *testing.T) {
	if _, err := NewSentryExporter(core.ExporterConfig{Type: "sentry", DSN: "not-a-dsn"}, logging.New()); err == nil {
		t.Fatal("expected error for invalid DSN")
	}
}

func TestSentryExporter_DSNParsing(t *testing.T) {
	dsn, err := parseDSN("https://abc123@o123.ingest.sentry.io/456")
	if err != nil {
		t.Fatal(err)
	}
	if dsn.protocol != "https" || dsn.publicKey != "abc123" ||
		dsn.host != "o123.ingest.sentry.io" || dsn.port != "" || dsn.projectID != "456" {
		t.Errorf("parsed DSN = %+v", dsn)
	}

	dsn, err = parseDSN("http://deadbeef@localhost:9000/sentry/7")
	if err != nil {
		t.Fatal(err)
	}
	if dsn.port != "9000" || dsn.path != "/sentry" || dsn.projectID != "7" {
		t.Errorf("parsed DSN = %+v", dsn)
	}
}

func TestSentryExporter_AlwaysSendsLog(t *testing.T) {
	e, bodies, auths := newSentryExporterForTest(t, false)

	evt := testEvent()
	if err := e.Export(evt); err != nil {
		t.Fatalf("Export: %v", err)
	}

	if len(*bodies) != 1 {
		t.Fatalf("envelopes sent = %d, want 1 (log only)", len(*bodies))
	}
	if !strings.Contains((*auths)[0], "sentry_key=abcdef0123456789") {
		t.Errorf("auth header = %q", (*auths)[0])
	}

	if !strings.HasSuffix((*bodies)[0], "\n") {
		t.Error("log envelope must end with a trailing newline")
	}
	env := parseEnvelope(t, (*bodies)[0])

	if env.item["type"] != "log" {
		t.Errorf("item type = %v, want log", env.item["type"])
	}
	if env.item["item_count"] != float64(1) {
		t.Errorf("item_count = %v, want 1", env.item["item_count"])
	}
	if env.item["content_type"] != "application/vnd.sentry.items.log+json" {
		t.Errorf("content_type = %v", env.item["content_type"])
	}

	items, ok := env.payload["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("payload items = %v, want 1 log item", env.payload["items"])
	}
	logItem := items[0].(map[string]any)

	if logItem["level"] != "info" {
		t.Errorf("level = %v, want info", logItem["level"])
	}
	if logItem["body"] != "MCP mcp:tools/call: add_todo" {
		t.Errorf("body = %v", logItem["body"])
	}
	if logItem["trace_id"] != TraceID(evt.GetSessionId()) {
		t.Errorf("trace_id = %v, want deterministic trace ID", logItem["trace_id"])
	}
	spanID := SpanID(evt.GetId())
	if logItem["event_id"] != spanID+spanID {
		t.Errorf("event_id = %v, want doubled span ID", logItem["event_id"])
	}

	attrs := logItem["attributes"].(map[string]any)
	eventType := attrs["eventType"].(map[string]any)
	if eventType["value"] != "mcp:tools/call" || eventType["type"] != "string" {
		t.Errorf("eventType attribute = %v", eventType)
	}
	duration := attrs["duration_ms"].(map[string]any)
	if duration["value"] != float64(150) || duration["type"] != "double" {
		t.Errorf("duration_ms attribute = %v", duration)
	}
}

func TestSentryExporter_TransactionWhenTracingEnabled(t *testing.T) {
	e, bodies, _ := newSentryExporterForTest(t, true)

	evt := testEvent()
	if err := e.Export(evt); err != nil {
		t.Fatalf("Export: %v", err)
	}

	if len(*bodies) != 2 {
		t.Fatalf("envelopes sent = %d, want 2 (log + transaction)", len(*bodies))
	}

	env := parseEnvelope(t, (*bodies)[1])
	if env.item["type"] != "transaction" {
		t.Fatalf("second envelope item type = %v, want transaction", env.item["type"])
	}
	tx := env.payload

	if tx["transaction"] != "mcp:tools/call - add_todo" {
		t.Errorf("transaction = %v", tx["transaction"])
	}
	contexts := tx["contexts"].(map[string]any)
	trace := contexts["trace"].(map[string]any)
	if trace["trace_id"] != TraceID(evt.GetSessionId()) {
		t.Errorf("trace_id = %v", trace["trace_id"])
	}
	if trace["span_id"] != SpanID(evt.GetId()) {
		t.Errorf("span_id = %v", trace["span_id"])
	}
	if trace["op"] != "mcp:tools/call" || trace["status"] != "ok" {
		t.Errorf("trace context = %v", trace)
	}
	// Customer properties surface as a custom agentcat context.
	agentcatCtx, ok := contexts["agentcat"].(map[string]any)
	if !ok || agentcatCtx["deployment"] != "canary" {
		t.Errorf("agentcat context = %v", contexts["agentcat"])
	}

	tags := tx["tags"].(map[string]any)
	if tags["source"] != "agentcat" {
		t.Errorf("tags.source = %v", tags["source"])
	}
	if tags["environment"] != "staging-env" || tags["release"] != "v1.2.3" {
		t.Errorf("environment/release tags = %v", tags)
	}
	// Customer tags namespaced.
	if tags["agentcat.env"] != "staging" || tags["agentcat.region"] != "us-east" {
		t.Errorf("customer tags = %v", tags)
	}

	// Duration-derived start timestamp.
	end := tx["timestamp"].(float64)
	start := tx["start_timestamp"].(float64)
	if end-start < 0.149 || end-start > 0.151 {
		t.Errorf("transaction duration = %v, want ~0.150", end-start)
	}
}

func TestSentryExporter_ErrorEventForIssueCreation(t *testing.T) {
	e, bodies, _ := newSentryExporterForTest(t, false)

	evt := testErrorEvent()
	if err := e.Export(evt); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Log + error event (no transaction: tracing disabled).
	if len(*bodies) != 2 {
		t.Fatalf("envelopes sent = %d, want 2 (log + error)", len(*bodies))
	}

	env := parseEnvelope(t, (*bodies)[1])
	if env.item["type"] != "event" {
		t.Fatalf("item type = %v, want event", env.item["type"])
	}
	errEvent := env.payload

	if errEvent["level"] != "error" {
		t.Errorf("level = %v", errEvent["level"])
	}
	exception := errEvent["exception"].(map[string]any)
	values := exception["values"].([]any)
	value := values[0].(map[string]any)
	if value["type"] != "NotFoundError" || value["value"] != "todo not found" {
		t.Errorf("exception value = %v", value)
	}
	mechanism := value["mechanism"].(map[string]any)
	if mechanism["type"] != "mcp_tool_call" || mechanism["handled"] != false {
		t.Errorf("mechanism = %v", mechanism)
	}

	contexts := errEvent["contexts"].(map[string]any)
	mcpCtx := contexts["mcp"].(map[string]any)
	if mcpCtx["resource_name"] != "add_todo" || mcpCtx["event_type"] != "mcp:tools/call" {
		t.Errorf("mcp context = %v", mcpCtx)
	}
	if errEvent["transaction"] != "mcp:tools/call - add_todo" {
		t.Errorf("transaction = %v", errEvent["transaction"])
	}
}

func TestSentryExporter_ErrorWithTracingCorrelatesTransaction(t *testing.T) {
	e, bodies, _ := newSentryExporterForTest(t, true)

	evt := testErrorEvent()
	if err := e.Export(evt); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Log + transaction + error event.
	if len(*bodies) != 3 {
		t.Fatalf("envelopes sent = %d, want 3", len(*bodies))
	}

	txEnv := parseEnvelope(t, (*bodies)[1])
	txTrace := txEnv.payload["contexts"].(map[string]any)["trace"].(map[string]any)
	if txTrace["status"] != "internal_error" {
		t.Errorf("transaction status = %v, want internal_error", txTrace["status"])
	}

	errEnv := parseEnvelope(t, (*bodies)[2])
	errTrace := errEnv.payload["contexts"].(map[string]any)["trace"].(map[string]any)
	if errTrace["parent_span_id"] != txTrace["span_id"] {
		t.Errorf("error parent_span_id = %v, want transaction span_id %v",
			errTrace["parent_span_id"], txTrace["span_id"])
	}
	if errTrace["trace_id"] != txTrace["trace_id"] {
		t.Errorf("error trace_id = %v, want transaction trace_id", errTrace["trace_id"])
	}
}
