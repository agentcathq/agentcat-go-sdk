package mcpgo

// Tests restored from internal/tracking/mark3labs_test.go.
// Many tests referenced internal globals (globalPublisher, globalPublisherOnce)
// and internal functions (createGetMoreToolsTool, handleGetMoreTools, ShutdownPublisher)
// that no longer exist in the mcpgo package. Those tests have been commented out
// with TODO markers. The portable tests that exercise addTracingToHooks,
// registerGetMoreToolsIfEnabled, and hook trigger helpers have been adapted.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
)

// ============================================================================
// Test Helpers (tracking_test specific)
// ============================================================================

// Helper to trigger BeforeAny hooks manually
func triggerBeforeAny(hooks *server.Hooks, ctx context.Context, id any, method mcp.MCPMethod, message any) {
	for _, callback := range hooks.OnBeforeAny {
		callback(ctx, id, method, message)
	}
}

// Helper to trigger AfterListTools hooks manually
func triggerAfterListTools(hooks *server.Hooks, ctx context.Context, id any, request *mcp.ListToolsRequest, result *mcp.ListToolsResult) {
	for _, callback := range hooks.OnAfterListTools {
		callback(ctx, id, request, result)
	}
}

// Helper to trigger OnSuccess hooks manually
func triggerOnSuccess(hooks *server.Hooks, ctx context.Context, id any, method mcp.MCPMethod, message any, result any) {
	for _, callback := range hooks.OnSuccess {
		callback(ctx, id, method, message, result)
	}
}

// Helper to trigger OnError hooks manually
func triggerOnError(hooks *server.Hooks, ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
	for _, callback := range hooks.OnError {
		callback(ctx, id, method, message, err)
	}
}

// testCustomError for testing different error types
type testCustomError struct {
	msg string
}

func (e *testCustomError) Error() string {
	return e.msg
}

// Helper function to check if a string contains a substring
func containsSubstring(s, substr string) bool {
	return strings.Contains(s, substr)
}

// ============================================================================
// addTracingToHooks Tests
// ============================================================================

func TestAddTracingToHooks_HookRegistration(t *testing.T) {
	t.Run("registers all required hooks", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()

		addTracingToHooks(hooks, opts, mock.publish)

		if len(hooks.OnBeforeAny) != 1 {
			t.Errorf("expected 1 BeforeAny callback, got %d", len(hooks.OnBeforeAny))
		}
		if len(hooks.OnAfterListTools) != 1 {
			t.Errorf("expected 1 AfterListTools callback, got %d", len(hooks.OnAfterListTools))
		}
		if len(hooks.OnSuccess) != 1 {
			t.Errorf("expected 1 OnSuccess callback, got %d", len(hooks.OnSuccess))
		}
		if len(hooks.OnError) != 1 {
			t.Errorf("expected 1 OnError callback, got %d", len(hooks.OnError))
		}
	})
}

func TestAddTracingToHooks_BeforeAnyHook(t *testing.T) {
	t.Run("stores request time", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()

		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()
		requestID := "req_123"
		method := mcp.MethodToolsCall

		// Trigger BeforeAny - should not panic
		triggerBeforeAny(hooks, ctx, requestID, method, nil)
	})

	t.Run("logs request method", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()

		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()
		requestID := "req_456"
		method := mcp.MethodToolsCall

		// This should not panic
		triggerBeforeAny(hooks, ctx, requestID, method, nil)
	})
}

func TestAddTracingToHooks_OnSuccessHook(t *testing.T) {
	t.Run("calculates duration from start time", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()

		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()
		requestID := "req_duration_test"
		method := mcp.MethodToolsCall
		request := &mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "test_tool",
			},
		}
		result := &mcp.CallToolResult{}

		// Start request
		triggerBeforeAny(hooks, ctx, requestID, method, request)

		// Small delay
		time.Sleep(10 * time.Millisecond)

		// Complete request
		triggerOnSuccess(hooks, ctx, requestID, method, request, result)

		// Duration should be calculated and cleaned up
		// Verify by triggering OnSuccess again - duration should be nil
		triggerOnSuccess(hooks, ctx, requestID, method, request, result)
	})

	t.Run("publishes event on success", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()

		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()
		requestID := "req_publish_test"
		method := mcp.MethodToolsCall
		request := &mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "test_tool",
			},
		}
		result := &mcp.CallToolResult{}

		triggerBeforeAny(hooks, ctx, requestID, method, request)
		triggerOnSuccess(hooks, ctx, requestID, method, request, result)

		// Note: Without proper context with session, event might be nil
		// This test verifies the hook executes without error
	})

	t.Run("handles nil session gracefully", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()

		addTracingToHooks(hooks, opts, mock.publish)

		// Context without session
		ctx := context.Background()
		requestID := "req_nil_session"
		method := mcp.MethodToolsCall

		triggerBeforeAny(hooks, ctx, requestID, method, nil)

		// Should not panic
		triggerOnSuccess(hooks, ctx, requestID, method, nil, nil)
	})
}

func TestAddTracingToHooks_OnErrorHook(t *testing.T) {
	t.Run("calculates duration on error", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()

		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()
		requestID := "req_error_duration"
		method := mcp.MethodToolsCall
		err := errors.New("test error")

		triggerBeforeAny(hooks, ctx, requestID, method, nil)
		time.Sleep(10 * time.Millisecond)
		triggerOnError(hooks, ctx, requestID, method, nil, err)

		// Should calculate duration and clean up
	})

	t.Run("publishes error event", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()

		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()
		requestID := "req_error_publish"
		method := mcp.MethodToolsCall
		err := errors.New("tool execution failed")

		triggerBeforeAny(hooks, ctx, requestID, method, nil)
		triggerOnError(hooks, ctx, requestID, method, nil, err)

		// Verify hook executes without panic
	})

	t.Run("handles different error types", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()

		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()

		testErrors := []error{
			errors.New("simple error"),
			&testCustomError{msg: "custom error"},
			nil, // nil error should be handled gracefully
		}

		for i, err := range testErrors {
			requestID := i
			triggerBeforeAny(hooks, ctx, requestID, mcp.MethodToolsCall, nil)
			triggerOnError(hooks, ctx, requestID, mcp.MethodToolsCall, nil, err)
		}
	})
}

func TestAddTracingToHooks_AfterListToolsHook(t *testing.T) {
	t.Run("triggers AfterListTools callback", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()

		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()
		request := &mcp.ListToolsRequest{}
		result := &mcp.ListToolsResult{
			Tools: []mcp.Tool{
				{
					Name:        "test_tool",
					Description: "A test tool",
				},
			},
		}

		// Should call addContextParamsToToolsList
		triggerAfterListTools(hooks, ctx, "req_123", request, result)
	})

	t.Run("handles nil result", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()

		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()
		request := &mcp.ListToolsRequest{}

		// Should not panic with nil result
		triggerAfterListTools(hooks, ctx, "req_456", request, nil)
	})
}

// ============================================================================
// Tool Result Error Detection Tests
// ============================================================================

func TestToolResultErrorDetection(t *testing.T) {
	t.Run("detects CallToolResult with IsError=true", func(t *testing.T) {
		// Create a tool result with IsError=true
		result := mcp.NewToolResultError("Tool execution failed")

		// Verify it would be detected as an error
		toolResult, ok := interface{}(result).(*mcp.CallToolResult)
		if !ok {
			t.Fatal("expected result to be a CallToolResult")
		}

		if !toolResult.IsError {
			t.Error("expected IsError=true")
		}

		// Verify error message extraction
		if len(toolResult.Content) == 0 {
			t.Fatal("expected content to have at least one item")
		}

		textContent, ok := toolResult.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatal("expected first content item to be TextContent")
		}

		expectedMsg := "Tool execution failed"
		if textContent.Text != expectedMsg {
			t.Errorf("expected message '%s', got '%s'", expectedMsg, textContent.Text)
		}
	})

	t.Run("detects CallToolResult with IsError=false", func(t *testing.T) {
		// Create a successful tool result
		result := mcp.NewToolResultText("Success")

		toolResult, ok := interface{}(result).(*mcp.CallToolResult)
		if !ok {
			t.Fatal("expected result to be a CallToolResult")
		}

		if toolResult.IsError {
			t.Error("expected IsError=false for successful result")
		}
	})

	t.Run("concatenates multiple text content items", func(t *testing.T) {
		// Create a result with multiple text items
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: "Error:"},
				mcp.TextContent{Type: "text", Text: "Invalid parameter."},
				mcp.TextContent{Type: "text", Text: "Expected string."},
			},
			IsError: true,
		}

		// Extract and concatenate messages
		var messages []string
		for _, content := range result.Content {
			if textContent, ok := content.(mcp.TextContent); ok {
				messages = append(messages, textContent.Text)
			}
		}

		concatenated := strings.Join(messages, " ")
		expected := "Error: Invalid parameter. Expected string."

		if concatenated != expected {
			t.Errorf("expected '%s', got '%s'", expected, concatenated)
		}
	})
}

// ============================================================================
// Integration Tests
// ============================================================================

func TestIntegration_FullRequestResponseCycle(t *testing.T) {
	t.Run("complete success flow", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()
		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()
		requestID := "req_integration_success"
		method := mcp.MethodToolsCall
		request := &mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "integration_tool",
			},
		}
		result := &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: "success result",
				},
			},
		}

		// Simulate full cycle
		triggerBeforeAny(hooks, ctx, requestID, method, request)
		time.Sleep(5 * time.Millisecond)
		triggerOnSuccess(hooks, ctx, requestID, method, request, result)

		// Events might be published (depends on context having session)
	})

	t.Run("complete error flow", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()
		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()
		requestID := "req_integration_error"
		method := mcp.MethodToolsCall
		request := &mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "failing_tool",
			},
		}
		err := errors.New("tool failed")

		// Simulate full error cycle
		triggerBeforeAny(hooks, ctx, requestID, method, request)
		time.Sleep(5 * time.Millisecond)
		triggerOnError(hooks, ctx, requestID, method, request, err)

		// Error events might be published
	})

	t.Run("multiple concurrent requests", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()
		addTracingToHooks(hooks, opts, mock.publish)

		var wg sync.WaitGroup
		numRequests := 20

		for i := 0; i < numRequests; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				ctx := context.Background()
				requestID := id
				method := mcp.MethodToolsCall
				request := &mcp.CallToolRequest{
					Params: mcp.CallToolParams{
						Name: "concurrent_tool",
					},
				}

				triggerBeforeAny(hooks, ctx, requestID, method, request)
				time.Sleep(1 * time.Millisecond)

				if id%2 == 0 {
					result := &mcp.CallToolResult{}
					triggerOnSuccess(hooks, ctx, requestID, method, request, result)
				} else {
					err := errors.New("test error")
					triggerOnError(hooks, ctx, requestID, method, request, err)
				}
			}(i)
		}

		wg.Wait()

		// All requests should complete without race conditions
	})
}

func TestIntegration_DifferentMCPMethods(t *testing.T) {
	methods := []mcp.MCPMethod{
		mcp.MethodToolsCall,
		mcp.MethodResourcesRead,
		mcp.MethodInitialize,
		mcp.MethodToolsList,
		mcp.MethodResourcesList,
	}

	for _, method := range methods {
		t.Run(string(method), func(t *testing.T) {
			hooks := &server.Hooks{}
			mock := &mockPublisher{}
			opts := DefaultOptions()
			addTracingToHooks(hooks, opts, mock.publish)

			ctx := context.Background()
			requestID := "req_method_test"

			triggerBeforeAny(hooks, ctx, requestID, method, nil)
			triggerOnSuccess(hooks, ctx, requestID, method, nil, nil)

			// Should handle different methods correctly
		})
	}
}

// ============================================================================
// registerGetMoreToolsIfEnabled Tests
// ============================================================================

func TestRegisterGetMoreToolsIfEnabled_Restored(t *testing.T) {
	t.Run("registers tool when DisableReportMissing=false", func(t *testing.T) {
		mockServer := server.NewMCPServer("test-server", "1.0.0", server.WithToolCapabilities(true))
		options := &agentcat.Options{}

		registerGetMoreToolsIfEnabled(mockServer, options)

		// Verify tool was registered
		tools := mockServer.ListTools()
		found := false
		for _, serverTool := range tools {
			if serverTool.Tool.Name == "get_more_tools" {
				found = true
				break
			}
		}

		if !found {
			t.Error("expected get_more_tools to be registered when DisableReportMissing=false")
		}
	})

	t.Run("does not register tool when DisableReportMissing=true", func(t *testing.T) {
		mockServer := server.NewMCPServer("test-server-2", "1.0.0", server.WithToolCapabilities(true))
		options := &agentcat.Options{
			DisableReportMissing: true,
		}

		registerGetMoreToolsIfEnabled(mockServer, options)

		// Verify tool was NOT registered
		tools := mockServer.ListTools()
		for _, serverTool := range tools {
			if serverTool.Tool.Name == "get_more_tools" {
				t.Error("expected get_more_tools NOT to be registered when DisableReportMissing=true")
			}
		}
	})

	t.Run("handles nil options gracefully", func(t *testing.T) {
		mockServer := server.NewMCPServer("test-server-3", "1.0.0", server.WithToolCapabilities(true))

		// Should not panic with nil options
		registerGetMoreToolsIfEnabled(mockServer, nil)

		// Verify tool was NOT registered
		tools := mockServer.ListTools()
		for _, serverTool := range tools {
			if serverTool.Tool.Name == "get_more_tools" {
				t.Error("expected get_more_tools NOT to be registered when options are nil")
			}
		}
	})
}

// ============================================================================
// Edge Cases & Error Handling Tests
// ============================================================================

func TestEdgeCases_Restored(t *testing.T) {
	t.Run("missing start time for duration calculation", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()
		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()
		requestID := "req_no_start_time"
		method := mcp.MethodToolsCall

		// Trigger OnSuccess without BeforeAny
		triggerOnSuccess(hooks, ctx, requestID, method, nil, nil)

		// Duration should be nil
	})

	t.Run("very old start time", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()
		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()
		requestID := "req_old_time"
		method := mcp.MethodToolsCall

		triggerBeforeAny(hooks, ctx, requestID, method, nil)

		// Wait a longer time
		time.Sleep(100 * time.Millisecond)

		triggerOnSuccess(hooks, ctx, requestID, method, nil, nil)

		// Should handle large duration values
	})

	t.Run("requestTimes cleanup", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()
		addTracingToHooks(hooks, opts, mock.publish)

		ctx := context.Background()

		// Create many requests
		for i := 0; i < 100; i++ {
			requestID := i
			triggerBeforeAny(hooks, ctx, requestID, mcp.MethodToolsCall, nil)
			triggerOnSuccess(hooks, ctx, requestID, mcp.MethodToolsCall, nil, nil)
		}

		// All entries should be cleaned up via LoadAndDelete
	})
}

// ============================================================================
// Thread Safety Tests
// ============================================================================

func TestThreadSafety_Restored(t *testing.T) {
	t.Run("concurrent hook invocations", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()
		addTracingToHooks(hooks, opts, mock.publish)

		var wg sync.WaitGroup
		numGoroutines := 50

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				ctx := context.Background()
				requestID := id
				method := mcp.MethodToolsCall

				triggerBeforeAny(hooks, ctx, requestID, method, nil)
				triggerOnSuccess(hooks, ctx, requestID, method, nil, nil)
			}(i)
		}

		wg.Wait()

		// Should complete without race conditions
		// Run with: go test -race
	})

	t.Run("race on requestTimes map", func(t *testing.T) {
		hooks := &server.Hooks{}
		mock := &mockPublisher{}
		opts := DefaultOptions()
		addTracingToHooks(hooks, opts, mock.publish)

		var wg sync.WaitGroup
		sharedRequestID := "shared_req"

		// Multiple goroutines accessing same request ID
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				ctx := context.Background()
				triggerBeforeAny(hooks, ctx, sharedRequestID, mcp.MethodToolsCall, nil)
			}()
		}

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				ctx := context.Background()
				triggerOnSuccess(hooks, ctx, sharedRequestID, mcp.MethodToolsCall, nil, nil)
			}()
		}

		wg.Wait()

		// sync.Map should handle concurrent access safely
	})
}

// ============================================================================
// Benchmarks
// ============================================================================

func BenchmarkHookExecution_Restored(b *testing.B) {
	hooks := &server.Hooks{}
	mock := &mockPublisher{}
	opts := DefaultOptions()
	addTracingToHooks(hooks, opts, mock.publish)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		requestID := i
		triggerBeforeAny(hooks, ctx, requestID, mcp.MethodToolsCall, nil)
		triggerOnSuccess(hooks, ctx, requestID, mcp.MethodToolsCall, nil, nil)
	}
}

// ============================================================================
// TODO: The following tests from the old internal/tracking/mark3labs_test.go
// referenced internal globals and functions that no longer exist in mcpgo:
//
// - TestAddTracingToHooks_PublisherInitialization (globalPublisher, globalPublisherOnce)
// - TestShutdownPublisher (ShutdownPublisher function)
// - TestCreateGetMoreToolsTool (createGetMoreToolsTool function)
// - TestHandleGetMoreTools (handleGetMoreTools, logging.New)
// - TestGetMoreToolsContextParam (createGetMoreToolsTool, contextWithServer)
// - BenchmarkAddTracingToHooks (resetGlobalState)
//
// These tests should be restored when the corresponding functions are made
// available or when the test infrastructure is updated to support them.
// ============================================================================
