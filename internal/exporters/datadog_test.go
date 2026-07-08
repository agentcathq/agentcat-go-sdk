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

func newDatadogExporterForTest(t *testing.T, env string) (*DatadogExporter, *[]byte, *[]byte) {
	t.Helper()

	var logsBody, metricsBody []byte
	logsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("DD-API-KEY") != "dd-key" {
			t.Errorf("missing DD-API-KEY header on logs request")
		}
		b, _ := io.ReadAll(r.Body)
		logsBody = b
		w.WriteHeader(http.StatusAccepted)
	}))
	metricsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("DD-API-KEY") != "dd-key" {
			t.Errorf("missing DD-API-KEY header on metrics request")
		}
		b, _ := io.ReadAll(r.Body)
		metricsBody = b
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(logsSrv.Close)
	t.Cleanup(metricsSrv.Close)

	e, err := NewDatadogExporter(core.ExporterConfig{
		Type:    "datadog",
		APIKey:  "dd-key",
		Site:    "datadoghq.com",
		Service: "todo-service",
		Env:     env,
	}, logging.New())
	if err != nil {
		t.Fatal(err)
	}
	// The real intake URLs are derived from the site; point them at the test
	// servers instead.
	e.logsURL = logsSrv.URL
	e.metricsURL = metricsSrv.URL

	return e, &logsBody, &metricsBody
}

func TestDatadogExporter_RequiredConfig(t *testing.T) {
	cases := []core.ExporterConfig{
		{Type: "datadog", Site: "datadoghq.com", Service: "svc"},
		{Type: "datadog", APIKey: "k", Service: "svc"},
		{Type: "datadog", APIKey: "k", Site: "datadoghq.com"},
	}
	for _, cfg := range cases {
		if _, err := NewDatadogExporter(cfg, logging.New()); err == nil {
			t.Errorf("expected error for config %+v", cfg)
		}
	}
}

func TestDatadogExporter_URLsFromSite(t *testing.T) {
	e, err := NewDatadogExporter(core.ExporterConfig{
		Type: "datadog", APIKey: "k", Site: "https://datadoghq.eu/", Service: "svc",
	}, logging.New())
	if err != nil {
		t.Fatal(err)
	}
	if e.logsURL != "https://http-intake.logs.datadoghq.eu/api/v2/logs" {
		t.Errorf("logsURL = %q", e.logsURL)
	}
	if e.metricsURL != "https://api.datadoghq.eu/api/v1/series" {
		t.Errorf("metricsURL = %q", e.metricsURL)
	}
}

func TestDatadogExporter_LogPayload(t *testing.T) {
	e, logsBody, _ := newDatadogExporterForTest(t, "staging-env")

	evt := testEvent()
	if err := e.Export(evt); err != nil {
		t.Fatalf("Export: %v", err)
	}

	var logs []datadogLog
	if err := json.Unmarshal(*logsBody, &logs); err != nil {
		t.Fatalf("unmarshal logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
	logEntry := logs[0]

	if logEntry.Message != "mcp:tools/call - add_todo" {
		t.Errorf("message = %q", logEntry.Message)
	}
	if logEntry.Service != "todo-service" {
		t.Errorf("service = %q", logEntry.Service)
	}
	if logEntry.DDSource != "agentcat" {
		t.Errorf("ddsource = %q, want agentcat", logEntry.DDSource)
	}
	if logEntry.Status != "info" {
		t.Errorf("status = %q, want info", logEntry.Status)
	}

	// Deterministic decimal trace/span IDs.
	if logEntry.DD == nil {
		t.Fatal("dd trace info missing")
	}
	if logEntry.DD.TraceID != DatadogTraceID(evt.GetSessionId()) {
		t.Errorf("dd.trace_id = %q, want %q", logEntry.DD.TraceID, DatadogTraceID(evt.GetSessionId()))
	}
	if logEntry.DD.SpanID != DatadogSpanID(evt.GetId()) {
		t.Errorf("dd.span_id = %q, want %q", logEntry.DD.SpanID, DatadogSpanID(evt.GetId()))
	}

	// ddtags: env, event_type (slashes -> dots), resource, source, and
	// namespaced customer tags.
	for _, want := range []string{
		"env:staging-env",
		"event_type:mcp:tools.call",
		"resource:add_todo",
		"source:agentcat",
		"agentcat.env:staging",
		"agentcat.region:us-east",
	} {
		if !strings.Contains(logEntry.DDTags, want) {
			t.Errorf("ddtags %q missing %q", logEntry.DDTags, want)
		}
	}

	// Full mcp.* object.
	if logEntry.MCP["session_id"] != "ses_2ZyBQqANd3XrLplhrVwvNGCwt4r" {
		t.Errorf("mcp.session_id = %v", logEntry.MCP["session_id"])
	}
	if logEntry.MCP["event_type"] != "mcp:tools/call" {
		t.Errorf("mcp.event_type = %v", logEntry.MCP["event_type"])
	}
	if logEntry.MCP["resource"] != "add_todo" {
		t.Errorf("mcp.resource = %v", logEntry.MCP["resource"])
	}
	if logEntry.MCP["duration_ms"] != float64(150) {
		t.Errorf("mcp.duration_ms = %v", logEntry.MCP["duration_ms"])
	}
	if logEntry.MCP["actor_id"] != "user-42" {
		t.Errorf("mcp.actor_id = %v", logEntry.MCP["actor_id"])
	}
	tags, ok := logEntry.MCP["tags"].(map[string]any)
	if !ok || tags["env"] != "staging" {
		t.Errorf("mcp.tags = %v", logEntry.MCP["tags"])
	}
	props, ok := logEntry.MCP["properties"].(map[string]any)
	if !ok || props["deployment"] != "canary" {
		t.Errorf("mcp.properties = %v", logEntry.MCP["properties"])
	}
}

func TestDatadogExporter_TagSanitization(t *testing.T) {
	e, logsBody, _ := newDatadogExporterForTest(t, "")

	evt := testEvent()
	evt.Tags = &map[string]string{"My Key: A,B": "x,y"}
	if err := e.Export(evt); err != nil {
		t.Fatalf("Export: %v", err)
	}

	var logs []datadogLog
	if err := json.Unmarshal(*logsBody, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs[0].DDTags, "agentcat.my_key_a_b:x_y") {
		t.Errorf("ddtags = %q, want sanitized customer tag agentcat.my_key_a_b:x_y", logs[0].DDTags)
	}
}

func TestDatadogExporter_MetricsSeries(t *testing.T) {
	e, _, metricsBody := newDatadogExporterForTest(t, "prod")

	evt := testErrorEvent()
	if err := e.Export(evt); err != nil {
		t.Fatalf("Export: %v", err)
	}

	var payload struct {
		Series []datadogMetric `json:"series"`
	}
	if err := json.Unmarshal(*metricsBody, &payload); err != nil {
		t.Fatalf("unmarshal metrics: %v", err)
	}

	byName := map[string]datadogMetric{}
	for _, m := range payload.Series {
		byName[m.Metric] = m
	}

	count, ok := byName["mcp.events.count"]
	if !ok || count.Type != "count" || count.Points[0][1] != 1 {
		t.Errorf("mcp.events.count series wrong: %+v", count)
	}
	duration, ok := byName["mcp.event.duration"]
	if !ok || duration.Type != "gauge" || duration.Points[0][1] != 150 {
		t.Errorf("mcp.event.duration series wrong: %+v", duration)
	}
	errCount, ok := byName["mcp.errors.count"]
	if !ok || errCount.Type != "count" || errCount.Points[0][1] != 1 {
		t.Errorf("mcp.errors.count series wrong: %+v", errCount)
	}

	// Timestamps are Unix seconds of the event timestamp.
	if count.Points[0][0] != 1783512000 {
		t.Errorf("metric timestamp = %v, want 1783512000", count.Points[0][0])
	}

	for _, want := range []string{"service:todo-service", "env:prod", "event_type:mcp:tools.call", "resource:add_todo"} {
		found := false
		for _, tag := range count.Tags {
			if tag == want {
				found = true
			}
		}
		if !found {
			t.Errorf("metric tags %v missing %q", count.Tags, want)
		}
	}
}

func TestDatadogExporter_ErrorLog(t *testing.T) {
	e, logsBody, _ := newDatadogExporterForTest(t, "")

	if err := e.Export(testErrorEvent()); err != nil {
		t.Fatalf("Export: %v", err)
	}

	var logs []datadogLog
	if err := json.Unmarshal(*logsBody, &logs); err != nil {
		t.Fatal(err)
	}
	logEntry := logs[0]
	if logEntry.Status != "error" {
		t.Errorf("status = %q, want error", logEntry.Status)
	}
	if !strings.Contains(logEntry.DDTags, "error:true") {
		t.Errorf("ddtags missing error:true: %q", logEntry.DDTags)
	}
	if logEntry.Error == nil || !strings.Contains(logEntry.Error.Message, "todo not found") {
		t.Errorf("root error missing or wrong: %+v", logEntry.Error)
	}
	if logEntry.MCP["is_error"] != true {
		t.Errorf("mcp.is_error = %v, want true", logEntry.MCP["is_error"])
	}
}

func TestDatadogExporter_PartialFailureReturnsError(t *testing.T) {
	e, _, _ := newDatadogExporterForTest(t, "")
	// Break the logs endpoint only.
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer failSrv.Close()
	e.logsURL = failSrv.URL

	if err := e.Export(testEvent()); err == nil {
		t.Fatal("expected error when logs endpoint fails")
	}
}
