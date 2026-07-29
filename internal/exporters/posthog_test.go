package exporters

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
)

var uuidv7Pattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestToUUIDv7_ValidFormat(t *testing.T) {
	id := "ses_" + ksuid.New().String()
	result := ToUUIDv7(id)
	if !uuidv7Pattern.MatchString(result) {
		t.Errorf("ToUUIDv7(%q) = %q, not a valid UUIDv7", id, result)
	}
}

func TestToUUIDv7_Deterministic(t *testing.T) {
	id := "ses_" + ksuid.New().String()
	a := ToUUIDv7(id)
	b := ToUUIDv7(id)
	c := ToUUIDv7(id)
	if a != b || b != c {
		t.Errorf("ToUUIDv7 not deterministic: %q %q %q", a, b, c)
	}
}

func TestToUUIDv7_DifferentInputsDiffer(t *testing.T) {
	a := ToUUIDv7("ses_" + ksuid.New().String())
	b := ToUUIDv7("ses_" + ksuid.New().String())
	if a == b {
		t.Errorf("different session IDs produced the same UUID: %q", a)
	}
}

func TestToUUIDv7_EmbedsKSUIDTimestamp(t *testing.T) {
	knownTime := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	payload := make([]byte, 16)
	for i := range payload {
		payload[i] = 0xab
	}
	k, err := ksuid.FromParts(knownTime, payload)
	if err != nil {
		t.Fatal(err)
	}
	result := ToUUIDv7("ses_" + k.String())

	hex := strings.ReplaceAll(result, "-", "")
	extractedMs, err := strconv.ParseInt(hex[:12], 16, 64)
	if err != nil {
		t.Fatal(err)
	}

	// KSUID has second-level precision, so the extracted timestamp should be
	// within 1 second of the known time.
	diff := extractedMs - knownTime.UnixMilli()
	if diff < -1000 || diff > 1000 {
		t.Errorf("embedded timestamp %d not within 1s of %d", extractedMs, knownTime.UnixMilli())
	}
}

func TestToUUIDv7_InvalidKSUIDFallsBack(t *testing.T) {
	result := ToUUIDv7("ses_invalid_ksuid_string")
	if !uuidv7Pattern.MatchString(result) {
		t.Errorf("ToUUIDv7 with invalid KSUID = %q, not a valid UUIDv7", result)
	}
}

func newPostHogExporterForTest(t *testing.T, enableAITracing bool) (*PostHogExporter, *[]byte) {
	t.Helper()

	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/batch" {
			t.Errorf("path = %q, want /batch", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		body = b
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	e, err := NewPostHogExporter(core.ExporterConfig{
		Type:            "posthog",
		APIKey:          "phc_test",
		Host:            srv.URL,
		EnableAITracing: enableAITracing,
	}, logging.New())
	if err != nil {
		t.Fatal(err)
	}
	return e, &body
}

type posthogBatchPayload struct {
	APIKey string                `json:"api_key"`
	Batch  []posthogCaptureEvent `json:"batch"`
}

func TestPostHogExporter_RequiresAPIKey(t *testing.T) {
	if _, err := NewPostHogExporter(core.ExporterConfig{Type: "posthog"}, logging.New()); err == nil {
		t.Fatal("expected error for missing apiKey")
	}
}

func TestPostHogExporter_DefaultHost(t *testing.T) {
	e, err := NewPostHogExporter(core.ExporterConfig{Type: "posthog", APIKey: "phc_x"}, logging.New())
	if err != nil {
		t.Fatal(err)
	}
	if e.batchURL != "https://us.i.posthog.com/batch" {
		t.Errorf("batchURL = %q", e.batchURL)
	}
}

func TestPostHogExporter_CaptureEvent(t *testing.T) {
	e, body := newPostHogExporterForTest(t, false)

	evt := testEvent()
	if err := e.Export(evt); err != nil {
		t.Fatalf("Export: %v", err)
	}

	var payload posthogBatchPayload
	if err := json.Unmarshal(*body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.APIKey != "phc_test" {
		t.Errorf("api_key = %q", payload.APIKey)
	}
	if len(payload.Batch) != 1 {
		t.Fatalf("batch = %d events, want 1", len(payload.Batch))
	}
	capture := payload.Batch[0]

	if capture.Event != "mcp_tool_call" {
		t.Errorf("event = %q, want mcp_tool_call", capture.Event)
	}
	if capture.Type != "capture" {
		t.Errorf("type = %q", capture.Type)
	}
	// distinct_id prefers the actor ID.
	if capture.DistinctID != "user-42" {
		t.Errorf("distinct_id = %q, want user-42", capture.DistinctID)
	}

	props := capture.Properties
	// $session_id is the deterministic UUIDv7 of the session KSUID.
	wantSession := ToUUIDv7(evt.GetSessionId())
	if props["$session_id"] != wantSession {
		t.Errorf("$session_id = %v, want %q", props["$session_id"], wantSession)
	}
	if !uuidv7Pattern.MatchString(props["$session_id"].(string)) {
		t.Errorf("$session_id = %v, not a UUIDv7", props["$session_id"])
	}
	if props["source"] != "agentcat" {
		t.Errorf("source = %v", props["source"])
	}
	if props["tool_name"] != "add_todo" || props["resource_name"] != "add_todo" {
		t.Errorf("tool/resource props = %v / %v", props["tool_name"], props["resource_name"])
	}
	if props["duration_ms"] != float64(150) {
		t.Errorf("duration_ms = %v", props["duration_ms"])
	}
	if props["parameters"].(map[string]any)["title"] != "buy milk" {
		t.Errorf("parameters = %v", props["parameters"])
	}

	// $set person properties from identity.
	set := props["$set"].(map[string]any)
	if set["name"] != "Ada Lovelace" {
		t.Errorf("$set.name = %v", set["name"])
	}

	// Customer tags/properties spread directly.
	if props["env"] != "staging" || props["region"] != "us-east" {
		t.Errorf("customer tags not spread: %v", props)
	}
	if props["deployment"] != "canary" {
		t.Errorf("customer properties not spread: %v", props)
	}
}

func TestPostHogExporter_CustomerTagsCanOverrideDefaults(t *testing.T) {
	e, body := newPostHogExporterForTest(t, false)

	evt := testEvent()
	evt.Properties = map[string]any{"source": "overridden"}
	if err := e.Export(evt); err != nil {
		t.Fatal(err)
	}

	var payload posthogBatchPayload
	if err := json.Unmarshal(*body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Batch[0].Properties["source"] != "overridden" {
		t.Errorf("source = %v, want customer override", payload.Batch[0].Properties["source"])
	}
}

func TestPostHogExporter_DistinctIDFallbacks(t *testing.T) {
	evt := testEvent()
	evt.IdentifyActorGivenId = nil
	if got := distinctID(evt); got != evt.GetSessionId() {
		t.Errorf("distinctID = %q, want session ID", got)
	}

	evt.SetSessionId("")
	if got := distinctID(evt); got != "anonymous" {
		t.Errorf("distinctID = %q, want anonymous", got)
	}
}

func TestPostHogExporter_ExceptionEvent(t *testing.T) {
	e, body := newPostHogExporterForTest(t, false)

	if err := e.Export(testErrorEvent()); err != nil {
		t.Fatal(err)
	}

	var payload posthogBatchPayload
	if err := json.Unmarshal(*body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Batch) != 2 {
		t.Fatalf("batch = %d events, want 2 (capture + $exception)", len(payload.Batch))
	}
	exception := payload.Batch[1]
	if exception.Event != "$exception" {
		t.Fatalf("second event = %q, want $exception", exception.Event)
	}
	props := exception.Properties
	if props["$exception_source"] != "backend" {
		t.Errorf("$exception_source = %v", props["$exception_source"])
	}
	if props["$exception_message"] != "todo not found" {
		t.Errorf("$exception_message = %v", props["$exception_message"])
	}
	if props["$exception_type"] != "NotFoundError" {
		t.Errorf("$exception_type = %v", props["$exception_type"])
	}
	if props["$exception_stacktrace"] != "todo not found\n\tat handler" {
		t.Errorf("$exception_stacktrace = %v", props["$exception_stacktrace"])
	}
}

func TestPostHogExporter_AISpanEvent(t *testing.T) {
	e, body := newPostHogExporterForTest(t, true)

	evt := testEvent()
	if err := e.Export(evt); err != nil {
		t.Fatal(err)
	}

	var payload posthogBatchPayload
	if err := json.Unmarshal(*body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Batch) != 2 {
		t.Fatalf("batch = %d events, want 2 (capture + $ai_span)", len(payload.Batch))
	}
	span := payload.Batch[1]
	if span.Event != "$ai_span" {
		t.Fatalf("second event = %q, want $ai_span", span.Event)
	}
	props := span.Properties
	if props["$ai_trace_id"] != ToUUIDv7(evt.GetSessionId()) {
		t.Errorf("$ai_trace_id = %v", props["$ai_trace_id"])
	}
	if props["$ai_span_id"] != ToUUIDv7(evt.GetId()) {
		t.Errorf("$ai_span_id = %v", props["$ai_span_id"])
	}
	if props["$ai_span_name"] != "add_todo" {
		t.Errorf("$ai_span_name = %v", props["$ai_span_name"])
	}
	if props["$ai_session_id"] != "agentcat_"+evt.GetSessionId() {
		t.Errorf("$ai_session_id = %v", props["$ai_session_id"])
	}
	if props["$ai_latency"] != 0.15 {
		t.Errorf("$ai_latency = %v, want 0.15", props["$ai_latency"])
	}
	if props["$ai_input_state"].(map[string]any)["title"] != "buy milk" {
		t.Errorf("$ai_input_state = %v", props["$ai_input_state"])
	}
	if props["$ai_output_state"] == nil {
		t.Error("$ai_output_state missing")
	}
	if props["$ai_is_error"] != false {
		t.Errorf("$ai_is_error = %v", props["$ai_is_error"])
	}
}

func TestPostHogExporter_NoAISpanForNonToolCall(t *testing.T) {
	e, body := newPostHogExporterForTest(t, true)

	evt := testEvent()
	evt.EventType = strPtr("mcp:resources/read")
	if err := e.Export(evt); err != nil {
		t.Fatal(err)
	}

	var payload posthogBatchPayload
	if err := json.Unmarshal(*body, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Batch) != 1 {
		t.Fatalf("batch = %d events, want 1 (no $ai_span for non tool call)", len(payload.Batch))
	}
	if payload.Batch[0].Event != "mcp_resource_read" {
		t.Errorf("event = %q, want mcp_resource_read", payload.Batch[0].Event)
	}
}

func TestMapEventType(t *testing.T) {
	cases := map[string]string{
		"mcp:tools/call":       "mcp_tool_call",
		"mcp:tools/list":       "mcp_tools_list",
		"mcp:initialize":       "mcp_initialize",
		"mcp:resources/read":   "mcp_resource_read",
		"mcp:resources/list":   "mcp_resources_list",
		"mcp:prompts/get":      "mcp_prompt_get",
		"mcp:prompts/list":     "mcp_prompts_list",
		"mcp:logging/setLevel": "mcp_logging_setLevel",
	}
	for in, want := range cases {
		if got := mapEventType(in); got != want {
			t.Errorf("mapEventType(%q) = %q, want %q", in, got, want)
		}
	}
}
