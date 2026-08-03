package mcpgo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk/v2"
)

// TestErrorTracking_ToolResultWithIsError verifies that calling a tool which
// returns mcp.NewToolResultError (IsError=true) is correctly surfaced to the
// caller and does not cause the tracking layer to panic or swallow the result.
func TestErrorTracking_ToolResultWithIsError(t *testing.T) {
	opts := &Options{}
	h := newHarness(t, opts)

	result := h.callTool("get_todo", map[string]any{"id": "nonexistent"})

	if !result.IsError {
		t.Errorf("expected result.IsError to be true for a nonexistent todo, got false")
	}

	text := resultText(result)
	assertContains(t, text, "not found")
}

// TestErrorTracking_ToolResultSuccess verifies that a successful tool call
// returns IsError=false and the expected content.
func TestErrorTracking_ToolResultSuccess(t *testing.T) {
	opts := &Options{}
	h := newHarness(t, opts)

	result := h.callTool("add_todo", map[string]any{
		"title":       "Buy milk",
		"description": "From the grocery store",
	})

	if result.IsError {
		t.Errorf("expected result.IsError to be false for a valid add_todo call, got true")
	}

	text := resultText(result)
	assertContains(t, text, "Buy milk")
}

// TestErrorTracking_InvalidToolName creates a server and client manually
// (without the test harness) and calls a tool that does not exist. The MCP
// protocol layer may return an error at the transport level. Regardless of how
// the error manifests, the tracking hooks must handle it without panicking.
func TestErrorTracking_InvalidToolName(t *testing.T) {
	mcpServer, _ := CreateFullServer()

	opts := &Options{}
	_, err := Track(mcpServer, "test_project", opts)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	defer unregisterServer(mcpServer)

	mcpClient, err := client.NewInProcessClient(mcpServer)
	if err != nil {
		t.Fatalf("NewInProcessClient failed: %v", err)
	}
	defer mcpClient.Close()

	ctx := context.Background()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}
	_, err = mcpClient.Initialize(ctx, initReq)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "nonexistent_tool"
	callReq.Params.Arguments = map[string]any{}

	// The call may return a transport-level error or a result with IsError.
	// Either outcome is acceptable; the key assertion is that the tracking
	// hooks do not panic.
	result, err := mcpClient.CallTool(ctx, callReq)
	if err != nil {
		// A protocol-level error is an acceptable outcome for an unknown tool.
		t.Logf("CallTool returned error (expected for unknown tool): %v", err)
		return
	}

	// If we got a result instead of an error, it should indicate failure.
	if result != nil && result.IsError {
		text := resultText(result)
		t.Logf("CallTool returned IsError result: %s", text)
	}
}

// TestErrorTracking_MissingRequiredParam verifies that omitting a required
// parameter (e.g. "title" for add_todo) produces IsError=true on the result.
func TestErrorTracking_MissingRequiredParam(t *testing.T) {
	opts := &Options{}
	h := newHarness(t, opts)

	result := h.callTool("add_todo", map[string]any{})

	if !result.IsError {
		t.Errorf("expected result.IsError to be true when required param 'title' is missing, got false")
	}
}

// TestFailingToolCall_HasInAppFrames verifies that a tool error captured from
// user code carries a stack with in-app frames and none of the SDK's own.
func TestFailingToolCall_HasInAppFrames(t *testing.T) {
	mcpServer := server.NewMCPServer(
		"test-server", "1.0.0",
		server.WithToolCapabilities(true),
	)

	failTool := mcp.NewTool("always_fail", mcp.WithDescription("A tool that always fails"))
	mcpServer.AddTool(failTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("something went wrong in user code"), nil
	})

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	}
	instance := newTestInstance(mcpServer, "test_project", opts)
	agentcat.RegisterServer(mcpServer, instance)
	t.Cleanup(func() { unregisterServer(mcpServer) })

	mock := &mockPublisher{}
	installTracking(mcpServer, instance, opts, mock.publish)

	result := callToolRaw(t, mcpServer, "always_fail", map[string]any{})
	if !result.IsError {
		t.Fatal("expected tool result to be an error")
	}

	events := mock.waitForEvents(1, 3*time.Second)
	toolEvents := filterEvents(events, "mcp:tools/call")
	if len(toolEvents) == 0 {
		t.Fatalf("no tools/call event found, got %d events: %v", len(events), eventTypes(events))
	}

	toolEvt := toolEvents[0]
	if toolEvt.IsError == nil || !*toolEvt.IsError {
		t.Fatal("expected event to be marked as error")
	}
	if toolEvt.Error == nil {
		t.Fatal("expected error data on event")
	}

	frames, ok := toolEvt.Error["frames"].([]map[string]any)
	if !ok || len(frames) == 0 {
		t.Fatal("expected non-empty frames in error data")
	}

	// No frame belongs to AgentCat SDK internals (they should be skipped).
	for _, f := range frames {
		fn, _ := f["function"].(string)
		if strings.Contains(fn, "go.agentcat.com/sdk/v2/internal") {
			t.Errorf("AgentCat internal frame should be skipped, found: %s", fn)
		}
	}

	// At least one frame is marked in_app=true.
	hasInApp := false
	for _, f := range frames {
		if inApp, ok := f["in_app"].(bool); ok && inApp {
			hasInApp = true
			break
		}
	}
	if !hasInApp {
		t.Error("expected at least one frame with in_app=true")
	}

	// No frame from runtime or testing packages exists (they should be skipped).
	for _, f := range frames {
		fn, _ := f["function"].(string)
		pkg := extractPackageFromFunc(fn)
		if pkg == "runtime" || strings.HasPrefix(pkg, "runtime/") || pkg == "testing" {
			t.Errorf("runtime/testing frame should be skipped, found: %s", fn)
		}
	}
}

// extractPackageFromFunc extracts the package path from a qualified function
// name. Test-local helper to avoid depending on internal packages.
// Keep in sync with internal/exceptions.extractPackage.
func extractPackageFromFunc(funcName string) string {
	if idx := strings.Index(funcName, ".("); idx > 0 {
		return funcName[:idx]
	}
	if idx := strings.LastIndex(funcName, "."); idx > 0 {
		return funcName[:idx]
	}
	return funcName
}
