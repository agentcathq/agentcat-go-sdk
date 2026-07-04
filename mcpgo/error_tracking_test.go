package mcpgo

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
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
