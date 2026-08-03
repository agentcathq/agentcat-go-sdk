package mcpgo

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk/v2"
)

// TestHTTP_ExtraDataCaptured verifies transport-layer metadata (HTTP headers)
// reaches the published tool-call event.
func TestHTTP_ExtraDataCaptured(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	}

	mcpClient, mock := setupSpyHTTP(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "add_todo"
	req.Params.Arguments = map[string]any{
		"title": "Test extra data",
	}

	if _, err := mcpClient.CallTool(ctx, req); err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	events := mock.waitForEvents(1, 3*time.Second)
	toolEvents := filterEvents(events, "mcp:tools/call")
	if len(toolEvents) == 0 {
		t.Fatalf("expected at least one mcp:tools/call event, got %d total events: %v", len(events), eventTypes(events))
	}

	evt := toolEvents[0]
	if evt.Parameters == nil {
		t.Fatal("expected non-nil parameters")
	}

	extra, ok := evt.Parameters["extra"].(map[string]any)
	if !ok {
		t.Fatalf("expected parameters to have 'extra' key with map value, got parameters: %v", evt.Parameters)
	}

	header, ok := extra["header"].(http.Header)
	if !ok {
		t.Fatalf("expected extra to have 'header' key with http.Header value, got extra: %v", extra)
	}

	// HTTP transport should populate standard headers like Content-Type
	if header.Get("Content-Type") == "" {
		t.Error("expected Content-Type header to be present in extra data")
	}
}

// TestErrorDetailsJoinTextBlocks pins the error message recorded for an
// IsError result built from several text blocks: they are joined in order.
func TestErrorDetailsJoinTextBlocks(t *testing.T) {
	h := newSpyHarness(t, nil)

	_, evt := h.call("multi_error", map[string]any{})

	if evt.IsError == nil || !*evt.IsError {
		t.Fatal("expected the event to be marked as an error")
	}
	if evt.Error == nil {
		t.Fatal("expected error data on the event")
	}
	if msg, _ := evt.Error["message"].(string); msg != "Error: Invalid parameter. Expected string." {
		t.Errorf("error message = %q, want the joined text blocks", msg)
	}
}

// TestErrorDetailsReadPointerTextContent pins that error details survive when a
// handler writes *mcp.TextContent: isContent has a value receiver, so both
// forms are legal content and either can carry the message.
func TestErrorDetailsReadPointerTextContent(t *testing.T) {
	h := newSpyHarness(t, nil)

	_, evt := h.call("pointer_error", map[string]any{})

	if evt.IsError == nil || !*evt.IsError {
		t.Fatal("expected the event to be marked as an error")
	}
	if evt.Error == nil {
		t.Fatal("expected error data on the event")
	}
	if msg, _ := evt.Error["message"].(string); msg != "pointer failure" {
		t.Errorf("error message = %q, want the pointer content's text", msg)
	}
}

// TestSuccessfulResultIsNotAnError is the negative-space mirror of the above:
// a normal result records no error and keeps its response.
func TestSuccessfulResultIsNotAnError(t *testing.T) {
	h := newSpyHarness(t, nil)

	_, evt := h.call("add_todo", map[string]any{"title": "fine"})

	if evt.IsError != nil && *evt.IsError {
		t.Error("successful call must not be recorded as an error")
	}
	if evt.Response == nil {
		t.Fatal("successful call must record its response")
	}
	if isErr, _ := evt.Response["isError"].(bool); isErr {
		t.Error("recorded response must carry isError=false")
	}
}

// TestEventRecordsCallDuration pins that the event's duration measures the
// dispatch: the middleware times the call itself, with no request-time map
// to fall out of sync.
func TestEventRecordsCallDuration(t *testing.T) {
	h := newSpyHarness(t, nil)

	_, evt := h.call("slow_tool", map[string]any{})

	if evt.Duration == nil {
		t.Fatal("expected a duration on the tool-call event")
	}
	if *evt.Duration < 20 {
		t.Errorf("duration = %dms, want at least the tool's own 25ms", *evt.Duration)
	}
}

// TestGetServerInfo_ReadsPrivateNameAndVersion is the change-detection
// tripwire for the sanctioned reflective read of mcp-go's unexported
// MCPServer.name/version fields.
func TestGetServerInfo_ReadsPrivateNameAndVersion(t *testing.T) {
	mcpServer := server.NewMCPServer("tripwire-server", "9.8.7")

	name, version := getServerInfo(mcpServer)
	if name != "tripwire-server" || version != "9.8.7" {
		t.Errorf("getServerInfo = (%q, %q), want (tripwire-server, 9.8.7) — mcp-go's private fields may have been renamed", name, version)
	}
}

func TestGetServerInfo_NilServerIsSafe(t *testing.T) {
	name, version := getServerInfo(nil)
	if name != "" || version != "" {
		t.Errorf("getServerInfo(nil) = (%q, %q), want empty strings", name, version)
	}
}

// TestInputSchemaValidationAcceptsInjectedParams pins that a server running
// mcp-go's input schema validation still accepts the injected handles: the
// validator runs against the REGISTERED (uninjected) schema, which permits
// unknown properties unless the customer closes it.
func TestInputSchemaValidationAcceptsInjectedParams(t *testing.T) {
	mcpServer := server.NewMCPServer("validating-server", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithInputSchemaValidation(),
	)
	registerHandleFixtures(mcpServer)

	opts := DefaultOptions()
	instance := newTestInstance(mcpServer, "test_project", opts)
	agentcat.RegisterServer(mcpServer, instance)
	t.Cleanup(func() { unregisterServer(mcpServer) })
	mock := &mockPublisher{}
	installTracking(mcpServer, instance, opts, mock.publish)

	result := callToolRaw(t, mcpServer, "echo_args", map[string]any{
		"payload": "keep", "session_id": sid("v"), "context": "why",
	})
	if result.IsError {
		t.Fatalf("input validation rejected the injected params: %q", resultText(result))
	}
	echoed := decodeEchoedArgs(t, result)
	if _, has := echoed["session_id"]; has {
		t.Error("handler must not see the injected session_id")
	}
	if echoed["payload"] != "keep" {
		t.Errorf("real argument lost: %v", echoed)
	}
	if len(filterEvents(mock.getEvents(), "mcp:tools/call")) != 1 {
		t.Errorf("expected one tool-call event, got %v", eventTypes(mock.getEvents()))
	}
}
