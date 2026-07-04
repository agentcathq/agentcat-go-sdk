package mcpgo

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestGetMoreTools_RegisteredAndCallable verifies that when DisableReportMissing
// is false (the default), the get_more_tools tool appears in the tool list with the expected
// "context" parameter, an OpenWorldHint=true annotation, and returns a
// successful response when called.
func TestGetMoreTools_RegisteredAndCallable(t *testing.T) {
	h := newHarness(t, &Options{
		DisableToolCallContext: true,
	})

	ctx := context.Background()

	// List tools - should include get_more_tools
	toolsResult, err := h.Client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	found := false
	for _, tool := range toolsResult.Tools {
		if tool.Name == "get_more_tools" {
			found = true

			// Verify it has the context parameter
			if tool.InputSchema.Properties != nil {
				if _, hasCtx := tool.InputSchema.Properties["context"]; !hasCtx {
					t.Error("get_more_tools should have a 'context' parameter")
				}
			} else {
				t.Error("get_more_tools should have InputSchema.Properties defined")
			}

			// Verify context is in the required list
			contextRequired := false
			for _, req := range tool.InputSchema.Required {
				if req == "context" {
					contextRequired = true
					break
				}
			}
			if !contextRequired {
				t.Error("'context' should be in the required parameters list")
			}

			// Verify open-world hint annotation
			if tool.Annotations.OpenWorldHint == nil {
				t.Error("get_more_tools should have OpenWorldHint annotation set")
			} else if !*tool.Annotations.OpenWorldHint {
				t.Error("get_more_tools should have OpenWorldHint=true")
			}
			break
		}
	}
	if !found {
		t.Fatal("Expected 'get_more_tools' in tool list when DisableReportMissing=false")
	}

	// Call get_more_tools with a valid context string
	result := h.callTool("get_more_tools", map[string]any{
		"context": "I need a tool to parse CSV files",
	})

	if result.IsError {
		t.Error("get_more_tools should not return IsError=true for a valid call")
	}

	text := resultText(result)
	assertContains(t, text, "full tool list")
}

// TestGetMoreTools_NotRegisteredWhenDisabled verifies that when
// DisableReportMissing is true, the get_more_tools tool does NOT appear in
// the tool list.
func TestGetMoreTools_NotRegisteredWhenDisabled(t *testing.T) {
	h := newHarness(t, &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	})

	ctx := context.Background()

	toolsResult, err := h.Client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	for _, tool := range toolsResult.Tools {
		if tool.Name == "get_more_tools" {
			t.Error("get_more_tools should NOT be registered when DisableReportMissing=true")
		}
	}
}

// TestGetMoreTools_MissingContextReturnsError verifies that calling
// get_more_tools without the required 'context' parameter returns an error
// result (IsError=true).
func TestGetMoreTools_MissingContextReturnsError(t *testing.T) {
	h := newHarness(t, &Options{
		DisableToolCallContext: true,
	})

	// Call without the required 'context' parameter
	result := h.callTool("get_more_tools", map[string]any{})

	if !result.IsError {
		t.Error("Expected IsError=true when 'context' parameter is missing")
	}
}
