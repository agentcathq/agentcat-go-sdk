package exporters

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
)

func newOTLPTestServer(t *testing.T) (*httptest.Server, *[]byte, *[]string) {
	t.Helper()
	var body []byte
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = b
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &body, &paths
}

func TestOTLPExporter_RequiresEndpoint(t *testing.T) {
	_, err := NewOTLPExporter(core.ExporterConfig{Type: "otlp"}, logging.New())
	if err == nil {
		t.Fatal("expected error for missing endpoint")
	}
}

func TestOTLPExporter_AppendsTracesPath(t *testing.T) {
	e, err := NewOTLPExporter(core.ExporterConfig{Type: "otlp", Endpoint: "http://collector:4318/"}, logging.New())
	if err != nil {
		t.Fatal(err)
	}
	if e.endpoint != "http://collector:4318/v1/traces" {
		t.Errorf("endpoint = %q, want path appended", e.endpoint)
	}

	e2, _ := NewOTLPExporter(core.ExporterConfig{Type: "otlp", Endpoint: "http://collector:4318/v1/traces"}, logging.New())
	if e2.endpoint != "http://collector:4318/v1/traces" {
		t.Errorf("endpoint = %q, want unchanged", e2.endpoint)
	}
}

func TestOTLPExporter_PayloadShape(t *testing.T) {
	srv, body, paths := newOTLPTestServer(t)

	e, err := NewOTLPExporter(core.ExporterConfig{
		Type:     "otlp",
		Endpoint: srv.URL,
		Headers:  map[string]string{"X-Custom": "abc"},
	}, logging.New())
	if err != nil {
		t.Fatal(err)
	}

	evt := testEvent()
	if err := e.Export(evt); err != nil {
		t.Fatalf("Export: %v", err)
	}

	if len(*paths) != 1 || (*paths)[0] != "/v1/traces" {
		t.Fatalf("paths = %v, want single POST to /v1/traces", *paths)
	}

	var req struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []otlpAttribute `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				Scope otlpScope  `json:"scope"`
				Spans []otlpSpan `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal(*body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if len(req.ResourceSpans) != 1 || len(req.ResourceSpans[0].ScopeSpans) != 1 {
		t.Fatal("expected exactly one resourceSpans/scopeSpans entry")
	}

	resAttrs := attrMap(req.ResourceSpans[0].Resource.Attributes)
	if resAttrs["service.name"] != "todo-server" {
		t.Errorf("service.name = %q, want todo-server", resAttrs["service.name"])
	}
	if resAttrs["service.version"] != "0.9.0" {
		t.Errorf("service.version = %q, want 0.9.0", resAttrs["service.version"])
	}

	scope := req.ResourceSpans[0].ScopeSpans[0].Scope
	if scope.Name != "agentcat" {
		t.Errorf("scope.name = %q, want agentcat", scope.Name)
	}

	spans := req.ResourceSpans[0].ScopeSpans[0].Spans
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	span := spans[0]

	if span.TraceID != TraceID(evt.GetSessionId()) {
		t.Errorf("traceId = %q, want deterministic %q", span.TraceID, TraceID(evt.GetSessionId()))
	}
	if span.SpanID != SpanID(evt.GetId()) {
		t.Errorf("spanId = %q, want deterministic %q", span.SpanID, SpanID(evt.GetId()))
	}
	if span.Kind != 2 {
		t.Errorf("kind = %d, want 2 (SPAN_KIND_SERVER)", span.Kind)
	}
	if span.Name != "mcp:tools/call" {
		t.Errorf("name = %q, want mcp:tools/call", span.Name)
	}
	if span.Status.Code != 1 {
		t.Errorf("status.code = %d, want 1 (OK)", span.Status.Code)
	}
	if span.StartTimeUnixNano != "1783512000000000000" {
		t.Errorf("startTimeUnixNano = %q, want 1783512000000000000", span.StartTimeUnixNano)
	}
	if span.EndTimeUnixNano != "1783512000150000000" {
		t.Errorf("endTimeUnixNano = %q, want start + 150ms", span.EndTimeUnixNano)
	}

	attrs := attrMap(span.Attributes)
	expectations := map[string]string{
		"source":              "agentcat",
		"mcp.event_type":      "mcp:tools/call",
		"mcp.session_id":      "ses_2ZyBQqANd3XrLplhrVwvNGCwt4r",
		"mcp.project_id":      "proj_test",
		"mcp.resource_name":   "add_todo",
		"mcp.user_intent":     "add a todo item",
		"mcp.actor_id":        "user-42",
		"mcp.actor_name":      "Ada Lovelace",
		"mcp.client_name":     "test-client",
		"mcp.client_version":  "1.2.3",
		"agentcat.tag.env":    "staging",
		"agentcat.tag.region": "us-east",
	}
	for k, want := range expectations {
		if attrs[k] != want {
			t.Errorf("attribute %s = %q, want %q", k, attrs[k], want)
		}
	}

	var props map[string]any
	if err := json.Unmarshal([]byte(attrs["agentcat.properties"]), &props); err != nil {
		t.Fatalf("agentcat.properties is not JSON: %v", err)
	}
	if props["deployment"] != "canary" {
		t.Errorf("properties.deployment = %v, want canary", props["deployment"])
	}
}

func TestOTLPExporter_ErrorStatusAndEmptyAttributeFiltering(t *testing.T) {
	srv, body, _ := newOTLPTestServer(t)

	e, _ := NewOTLPExporter(core.ExporterConfig{Type: "otlp", Endpoint: srv.URL}, logging.New())

	evt := testErrorEvent()
	evt.UserIntent = nil // should be filtered out of attributes
	if err := e.Export(evt); err != nil {
		t.Fatalf("Export: %v", err)
	}

	var req otlpRequest
	if err := json.Unmarshal(*body, &req); err != nil {
		t.Fatal(err)
	}
	span := req.ResourceSpans[0].ScopeSpans[0].Spans[0]
	if span.Status.Code != 2 {
		t.Errorf("status.code = %d, want 2 (ERROR)", span.Status.Code)
	}
	attrs := attrMap(span.Attributes)
	if _, ok := attrs["mcp.user_intent"]; ok {
		t.Error("empty mcp.user_intent attribute should be filtered out")
	}
}

func TestOTLPExporter_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	e, _ := NewOTLPExporter(core.ExporterConfig{Type: "otlp", Endpoint: srv.URL}, logging.New())
	if err := e.Export(testEvent()); err == nil {
		t.Fatal("expected error for non-2xx response")
	}
}

func attrMap(attrs []otlpAttribute) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[a.Key] = a.Value.StringValue
	}
	return m
}
