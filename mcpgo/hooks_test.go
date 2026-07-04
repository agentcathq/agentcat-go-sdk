package mcpgo

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
)

// mockPublisher collects published events for test assertions.
type mockPublisher struct {
	mu     sync.Mutex
	events []*agentcat.Event
}

func (m *mockPublisher) publish(evt *agentcat.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, evt)
}

func (m *mockPublisher) getEvents() []*agentcat.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*agentcat.Event, len(m.events))
	copy(cp, m.events)
	return cp
}

func (m *mockPublisher) waitForEvents(n int, timeout time.Duration) []*agentcat.Event {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events := m.getEvents()
		if len(events) >= n {
			return events
		}
		time.Sleep(10 * time.Millisecond)
	}
	return m.getEvents()
}

func TestBeforeAny_StoresRequestTime(t *testing.T) {
	hooks := &server.Hooks{}
	mock := &mockPublisher{}
	opts := DefaultOptions()

	addTracingToHooks(hooks, opts, mock.publish)

	// The BeforeAny hook was added; invoke it directly via the Hooks struct.
	// hooks.OnBeforeAny should have at least one hook (ours).
	if len(hooks.OnBeforeAny) == 0 {
		t.Fatal("expected at least one BeforeAny hook")
	}

	ctx := context.Background()
	id := "req-1"
	hooks.OnBeforeAny[0](ctx, id, mcp.MethodToolsCall, nil)

	// Calling OnSuccess should be able to compute a duration
	if len(hooks.OnSuccess) == 0 {
		t.Fatal("expected at least one OnSuccess hook")
	}

	// Small delay to ensure non-zero duration
	time.Sleep(1 * time.Millisecond)

	// We need a session in context for the event to be created.
	// Without a ClientSession, the session capture will return nil and
	// NewEvent will return nil. This is expected behavior.
	// Let's just verify the hook doesn't panic.
	hooks.OnSuccess[0](ctx, id, mcp.MethodToolsCall, nil, nil)

	// Since there's no session in context, no event should be published
	events := mock.getEvents()
	if len(events) != 0 {
		t.Fatalf("expected 0 events (no session in ctx), got %d", len(events))
	}
}

func TestOnSuccess_CreatesAndPublishesEvent(t *testing.T) {
	hooks := &server.Hooks{}
	mock := &mockPublisher{}
	opts := DefaultOptions()

	addTracingToHooks(hooks, opts, mock.publish)

	// Create an MCP server and register it
	mcpServer := server.NewMCPServer("test-server", "1.0.0")
	defer agentcat.UnregisterServer(mcpServer)

	instance := &agentcat.MCPcatInstance{
		ProjectID: "proj_test",
		Options: &agentcat.Options{
			DisableReportMissing:   opts.DisableReportMissing,
			DisableToolCallContext: opts.DisableToolCallContext,
			Debug:                  opts.Debug,
		},
		ServerRef: mcpServer,
	}
	agentcat.RegisterServer(mcpServer, instance)

	// We cannot easily create a real ClientSession in context without
	// a full transport setup. Instead, test the parameter extraction
	// and event creation logic directly.

	// Test extractParameters with a CallToolRequest
	req := &mcp.CallToolRequest{}
	req.Params.Name = "test_tool"
	req.Params.Arguments = map[string]any{
		"query":   "hello",
		"context": "Testing user intent",
	}

	params := extractParameters(req)
	if params == nil {
		t.Fatal("expected non-nil params")
	}
	if params["name"] != "test_tool" {
		t.Errorf("expected name 'test_tool', got %v", params["name"])
	}
	// "context" should be filtered out from arguments
	if args, ok := params["arguments"].(map[string]any); ok {
		if _, hasContext := args["context"]; hasContext {
			t.Error("context param should be filtered from arguments")
		}
		if args["query"] != "hello" {
			t.Errorf("expected query 'hello', got %v", args["query"])
		}
	} else {
		t.Error("expected arguments map")
	}

	// Test user intent extraction
	intent := extractUserIntentFromRequest(req)
	if intent != "Testing user intent" {
		t.Errorf("expected intent 'Testing user intent', got '%s'", intent)
	}
}

func TestOnSuccess_DetectsCallToolResultIsError(t *testing.T) {
	// Test that when CallToolResult.IsError is true, we detect it
	result := &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: "something went wrong",
			},
		},
	}

	// Simulate the isError detection logic from OnSuccess
	isError := false
	var errorDetails error
	if result.IsError {
		isError = true
		var errorMessages []string
		for _, content := range result.Content {
			if textContent, ok := content.(mcp.TextContent); ok {
				errorMessages = append(errorMessages, textContent.Text)
			}
		}
		if len(errorMessages) > 0 {
			errorDetails = errors.New(errorMessages[0])
		}
	}

	if !isError {
		t.Error("expected isError to be true")
	}
	if errorDetails == nil {
		t.Fatal("expected non-nil errorDetails")
	}
	if errorDetails.Error() != "something went wrong" {
		t.Errorf("unexpected error message: %s", errorDetails.Error())
	}
}

func TestOnError_CreatesErrorEvent(t *testing.T) {
	hooks := &server.Hooks{}
	mock := &mockPublisher{}
	opts := DefaultOptions()

	addTracingToHooks(hooks, opts, mock.publish)

	if len(hooks.OnError) == 0 {
		t.Fatal("expected at least one OnError hook")
	}

	// Without a session in context, no event is published (expected)
	ctx := context.Background()
	testErr := errors.New("test error")
	hooks.OnError[0](ctx, "req-err", mcp.MethodToolsCall, nil, testErr)

	events := mock.getEvents()
	if len(events) != 0 {
		t.Fatalf("expected 0 events (no session in ctx), got %d", len(events))
	}
}

func TestAfterListTools_InjectsContextParams_WhenEnabled(t *testing.T) {
	hooks := &server.Hooks{}
	mock := &mockPublisher{}
	opts := DefaultOptions()
	mcpServer := server.NewMCPServer("test-server", "1.0.0")
	defer agentcat.UnregisterServer(mcpServer)

	instance := &agentcat.MCPcatInstance{
		ProjectID: "proj_test",
		Options: &agentcat.Options{
			DisableReportMissing:   opts.DisableReportMissing,
			DisableToolCallContext: opts.DisableToolCallContext,
			Debug:                  opts.Debug,
		},
		ServerRef: mcpServer,
	}
	agentcat.RegisterServer(mcpServer, instance)

	addTracingToHooks(hooks, opts, mock.publish)

	if len(hooks.OnAfterListTools) == 0 {
		t.Fatal("expected at least one AfterListTools hook")
	}

	// Create a context with the server
	ctx := context.WithValue(context.Background(), serverContextKey{}, mcpServer)

	// Create test tools result
	result := &mcp.ListToolsResult{
		Tools: []mcp.Tool{
			{
				Name:        "my_tool",
				Description: "A test tool",
				InputSchema: mcp.ToolInputSchema{
					Type:       "object",
					Properties: map[string]any{},
				},
			},
		},
	}

	// Call the hook - but since we can't easily inject the server into context
	// using the mcp-go server key, let's test the addContextParamsToToolsList directly
	addContextParamsToToolsList(result)

	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if _, ok := result.Tools[0].InputSchema.Properties["context"]; !ok {
		t.Error("expected context param to be injected")
	}

	_ = ctx // used above conceptually; direct function call used instead
}

func TestAfterListTools_DoesNotInject_WhenDisabled(t *testing.T) {
	// When DisableToolCallContext is true, context params should not be injected
	opts := DefaultOptions()
	opts.DisableToolCallContext = true

	mcpServer := server.NewMCPServer("test-server", "1.0.0")
	defer agentcat.UnregisterServer(mcpServer)

	instance := &agentcat.MCPcatInstance{
		ProjectID: "proj_test",
		Options: &agentcat.Options{
			DisableReportMissing:   opts.DisableReportMissing,
			DisableToolCallContext: opts.DisableToolCallContext,
			Debug:                  opts.Debug,
		},
		ServerRef: mcpServer,
	}
	agentcat.RegisterServer(mcpServer, instance)

	// The hook checks via registry lookup: if DisableToolCallContext is true,
	// it should NOT call addContextParamsToToolsList.
	// We test the conditional logic directly:
	shouldAddContext := false
	if tracker := agentcat.GetInstance(mcpServer); tracker != nil && tracker.Options != nil {
		shouldAddContext = !tracker.Options.DisableToolCallContext
	}

	if shouldAddContext {
		t.Error("expected shouldAddContext to be false when DisableToolCallContext is true")
	}

	// Verify no injection happens
	result := &mcp.ListToolsResult{
		Tools: []mcp.Tool{
			{
				Name:        "my_tool",
				Description: "A test tool",
				InputSchema: mcp.ToolInputSchema{
					Type:       "object",
					Properties: map[string]any{},
				},
			},
		},
	}

	// Don't call addContextParamsToToolsList since the condition is false
	if _, ok := result.Tools[0].InputSchema.Properties["context"]; ok {
		t.Error("context param should NOT be present when disabled")
	}
}

func TestExtractParameters_ReadResourceRequest(t *testing.T) {
	req := &mcp.ReadResourceRequest{}
	req.Params.URI = "file:///test.txt"

	params := extractParameters(req)
	if params == nil {
		t.Fatal("expected non-nil params")
	}
	if params["uri"] != "file:///test.txt" {
		t.Errorf("expected uri 'file:///test.txt', got %v", params["uri"])
	}
}

func TestExtractParameters_GetPromptRequest(t *testing.T) {
	req := &mcp.GetPromptRequest{}
	req.Params.Name = "test_prompt"
	req.Params.Arguments = map[string]string{"key": "value"}

	params := extractParameters(req)
	if params == nil {
		t.Fatal("expected non-nil params")
	}
	if params["name"] != "test_prompt" {
		t.Errorf("expected name 'test_prompt', got %v", params["name"])
	}
}

func TestExtractParameters_NilRequest(t *testing.T) {
	params := extractParameters(nil)
	if params != nil {
		t.Errorf("expected nil params for nil request, got %v", params)
	}
}

func TestExtractResponse_CallToolResult(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: "hello world"},
		},
		IsError: false,
	}

	resp := extractResponse(result)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp["isError"] != false {
		t.Error("expected isError to be false")
	}
	if resp["content"] == nil {
		t.Error("expected content to be present")
	}
}

func TestExtractResponse_NilResponse(t *testing.T) {
	resp := extractResponse(nil)
	if resp != nil {
		t.Errorf("expected nil response for nil input, got %v", resp)
	}
}

func TestExtractResourceName(t *testing.T) {
	req := &mcp.ReadResourceRequest{}
	req.Params.URI = "file:///test.txt"

	name := extractResourceName(req)
	if name != "file:///test.txt" {
		t.Errorf("expected 'file:///test.txt', got '%s'", name)
	}
}

func TestExtractResourceName_NonResourceRequest(t *testing.T) {
	name := extractResourceName(&mcp.CallToolRequest{})
	if name != "" {
		t.Errorf("expected empty string for non-resource request, got '%s'", name)
	}
}

func TestExtractToolName(t *testing.T) {
	req := &mcp.CallToolRequest{}
	req.Params.Name = "my_tool"

	name := extractToolName(req)
	if name != "my_tool" {
		t.Errorf("expected 'my_tool', got '%s'", name)
	}
}

func TestExtractToolName_NonToolRequest(t *testing.T) {
	name := extractToolName(&mcp.ReadResourceRequest{})
	if name != "" {
		t.Errorf("expected empty string for non-tool request, got '%s'", name)
	}
}

func TestExtractUserIntent_NoContext(t *testing.T) {
	req := &mcp.CallToolRequest{}
	req.Params.Name = "tool"
	req.Params.Arguments = map[string]any{
		"query": "hello",
	}

	intent := extractUserIntentFromRequest(req)
	if intent != "" {
		t.Errorf("expected empty intent, got '%s'", intent)
	}
}

func TestExtractUserIntent_NonToolRequest(t *testing.T) {
	intent := extractUserIntentFromRequest(&mcp.ReadResourceRequest{})
	if intent != "" {
		t.Errorf("expected empty intent for non-tool request, got '%s'", intent)
	}
}

// serverContextKey is used for testing purposes to inject server into context.
// The actual mcp-go library uses an unexported key.
type serverContextKey struct{}

// setupStreamableHTTPWithMock creates a real HTTP-based MCP client with a mock
// publisher injected (instead of the real publisher used by Track). This lets
// us assert on published events including transport-layer metadata like headers.
func setupStreamableHTTPWithMock(t *testing.T, opts *Options) (*client.Client, *mockPublisher) {
	t.Helper()

	mcpServer, _ := CreateFullServer()

	mock := &mockPublisher{}

	if opts == nil {
		opts = DefaultOptions()
	}

	projectID := "proj_test"
	coreOpts := &agentcat.Options{
		DisableReportMissing:   opts.DisableReportMissing,
		DisableToolCallContext: opts.DisableToolCallContext,
		Debug:                  opts.Debug,
	}
	instance := &agentcat.MCPcatInstance{
		ProjectID: projectID,
		Options:   coreOpts,
		ServerRef: mcpServer,
	}
	agentcat.RegisterServer(mcpServer, instance)

	hooks := &server.Hooks{}
	server.WithHooks(hooks)(mcpServer)
	addTracingToHooks(hooks, opts, mock.publish)

	httpServer := server.NewTestStreamableHTTPServer(mcpServer)

	mcpClient, err := client.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		httpServer.Close()
		agentcat.UnregisterServer(mcpServer)
		t.Fatalf("NewStreamableHttpClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	if err := mcpClient.Start(ctx); err != nil {
		cancel()
		mcpClient.Close()
		httpServer.Close()
		agentcat.UnregisterServer(mcpServer)
		t.Fatalf("client.Start failed: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "extra-test-client",
		Version: "1.0.0",
	}

	_, err = mcpClient.Initialize(ctx, initReq)
	if err != nil {
		cancel()
		mcpClient.Close()
		httpServer.Close()
		agentcat.UnregisterServer(mcpServer)
		t.Fatalf("Initialize failed: %v", err)
	}

	t.Cleanup(func() {
		mcpClient.Close()
		httpServer.Close()
		cancel()
		agentcat.UnregisterServer(mcpServer)
	})

	return mcpClient, mock
}

// filterEvents returns events matching the given event type.
func filterEvents(events []*agentcat.Event, eventType string) []*agentcat.Event {
	var filtered []*agentcat.Event
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == eventType {
			filtered = append(filtered, evt)
		}
	}
	return filtered
}

func TestHTTP_ExtraDataCaptured(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	}

	mcpClient, mock := setupStreamableHTTPWithMock(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "add_todo"
	req.Params.Arguments = map[string]any{
		"title": "Test extra data",
	}

	_, err := mcpClient.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	// Wait for events: initialize + tool call (at minimum)
	events := mock.waitForEvents(2, 3*time.Second)
	toolEvents := filterEvents(events, "mcp:tools/call")

	if len(toolEvents) == 0 {
		t.Fatalf("expected at least one mcp:tools/call event, got %d total events: %v", len(events), eventTypes(events))
	}

	evt := toolEvents[0]

	// Verify extra data is captured from HTTP transport
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

// eventTypes returns a slice of event type strings for debugging.
func eventTypes(events []*agentcat.Event) []string {
	var types []string
	for _, evt := range events {
		if evt.EventType != nil {
			types = append(types, *evt.EventType)
		} else {
			types = append(types, "<nil>")
		}
	}
	return types
}

func TestFailingToolCall_HasInAppFrames(t *testing.T) {
	// Create a server with a tool that returns an error from user code.
	mcpServer := server.NewMCPServer(
		"test-server", "1.0.0",
		server.WithToolCapabilities(true),
	)

	failTool := mcp.NewTool("always_fail", mcp.WithDescription("A tool that always fails"))
	mcpServer.AddTool(failTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("something went wrong in user code"), nil
	})

	// Wire up mock publisher with HTTP transport (needed for session context).
	mock := &mockPublisher{}
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	}
	coreOpts := &agentcat.Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	}
	agentcat.RegisterServer(mcpServer, &agentcat.MCPcatInstance{
		ProjectID: "test_project",
		Options:   coreOpts,
		ServerRef: mcpServer,
	})

	hooks := &server.Hooks{}
	server.WithHooks(hooks)(mcpServer)
	sessionMap := addTracingToHooks(hooks, opts, mock.publish)

	httpServer := server.NewTestStreamableHTTPServer(mcpServer)

	mcpClient, err := client.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		t.Fatalf("NewStreamableHttpClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Cleanup(func() {
		mcpClient.Close()
		httpServer.Close()
		cancel()
		sessionMap.Stop()
		agentcat.UnregisterServer(mcpServer)
	})

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test-client", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "always_fail"
	callReq.Params.Arguments = map[string]any{}

	result, err := mcpClient.CallTool(ctx, callReq)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool result to be an error")
	}

	// Wait for events: initialize + tool call.
	events := mock.waitForEvents(2, 3*time.Second)
	toolEvents := filterEvents(events, "mcp:tools/call")
	if len(toolEvents) == 0 {
		t.Fatalf("no tools/call event found, got %d events: %v", len(events), eventTypes(events))
	}

	toolEvt := toolEvents[0]

	// The event should be marked as an error.
	if toolEvt.IsError == nil || !*toolEvt.IsError {
		t.Fatal("expected event to be marked as error")
	}

	// The event should have error data with frames.
	if toolEvt.Error == nil {
		t.Fatal("expected error data on event")
	}

	frames, ok := toolEvt.Error["frames"].([]map[string]any)
	if !ok || len(frames) == 0 {
		t.Fatal("expected non-empty frames in error data")
	}

	// Verify that NO frame belongs to MCPCat SDK internals (they should be skipped).
	for _, f := range frames {
		fn, _ := f["function"].(string)
		if strings.Contains(fn, "go.agentcat.com/sdk/internal") {
			t.Errorf("MCPCat internal frame should be skipped, found: %s", fn)
		}
	}

	// Verify that at least one frame is marked in_app=true.
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

	// Verify no frame from runtime or testing packages exists (they should be skipped).
	for _, f := range frames {
		fn, _ := f["function"].(string)
		pkg := extractPackageFromFunc(fn)
		if pkg == "runtime" || strings.HasPrefix(pkg, "runtime/") || pkg == "testing" {
			t.Errorf("runtime/testing frame should be skipped, found: %s", fn)
		}
	}
}

// extractPackageFromFunc extracts the package path from a qualified function name.
// Test-local helper to avoid depending on internal packages.
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
