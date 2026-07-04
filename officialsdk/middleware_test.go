package officialsdk

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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

// createTestServer creates a server with a test tool and connects a client.
func createTestServer(t *testing.T, opts *Options) (*mcp.Server, *mcp.ClientSession, *mockPublisher, func()) {
	t.Helper()

	mock := &mockPublisher{}
	serverImpl := &mcp.Implementation{Name: "test-server", Version: "1.0.0"}
	server := mcp.NewServer(serverImpl, nil)

	// Add a test tool — uses typed API to match production behavior.
	mcp.AddTool(server, &mcp.Tool{
		Name:        "greet",
		Description: "Greets a person by name",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args greetArgs) (*mcp.CallToolResult, greetResult, error) {
		return nil, greetResult{Text: "Hello, " + args.Name + "!"}, nil
	})

	// Add a tool that returns an error
	mcp.AddTool(server, &mcp.Tool{
		Name:        "fail_tool",
		Description: "Always returns an error",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args failToolArgs) (*mcp.CallToolResult, greetResult, error) {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: "something went wrong"}},
		}, greetResult{}, nil
	})

	if opts == nil {
		opts = DefaultOptions()
	}

	// Install tracking middleware with our mock publisher
	projectID := "proj_test"
	coreOpts := &agentcat.Options{
		DisableReportMissing:       opts.DisableReportMissing,
		DisableToolCallContext:     opts.DisableToolCallContext,
		Debug:                      opts.Debug,
		RedactSensitiveInformation: opts.RedactSensitiveInformation,
	}
	instance := &agentcat.MCPcatInstance{
		ProjectID: projectID,
		Options:   coreOpts,
		ServerRef: server,
	}
	agentcat.RegisterServer(server, instance)

	middleware, sessionMap := newTrackingMiddleware(projectID, opts, mock.publish, serverImpl)
	defer sessionMap.Stop()
	server.AddReceivingMiddleware(middleware)

	// Register get_more_tools if enabled
	registerGetMoreToolsIfEnabled(server, coreOpts)

	// Connect server and client via in-memory transport
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"}, nil)
	clientSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}

	cleanup := func() {
		clientSession.Close()
		serverSession.Wait()
		agentcat.UnregisterServer(server)
	}

	return server, clientSession, mock, cleanup
}

func TestMiddleware_ToolCall_CreatesEvent(t *testing.T) {
	_, clientSession, mock, cleanup := createTestServer(t, nil)
	defer cleanup()

	ctx := context.Background()

	// Call the greet tool
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "World"},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	// Verify tool result
	if result.IsError {
		t.Error("expected tool call to succeed")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content")
	}
	if tc, ok := result.Content[0].(*mcp.TextContent); ok {
		// With mcp.AddTool(), the typed result struct is JSON-serialized.
		if tc.Text != `{"text":"Hello, World!"}` {
			t.Errorf("expected '{\"text\":\"Hello, World!\"}', got '%s'", tc.Text)
		}
	}

	// Wait for events to be captured asynchronously
	events := mock.waitForEvents(1, 2*time.Second)

	// Filter for tool call events (skip initialize and initialized)
	var toolEvents []*agentcat.Event
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == "mcp:tools/call" {
			toolEvents = append(toolEvents, evt)
		}
	}

	if len(toolEvents) == 0 {
		t.Fatal("expected at least one tool call event")
	}

	evt := toolEvents[0]
	if evt.EventType == nil || *evt.EventType != "mcp:tools/call" {
		t.Errorf("expected event type 'mcp:tools/call', got %v", evt.EventType)
	}
	if evt.Duration == nil || *evt.Duration < 0 {
		t.Error("expected non-negative duration")
	}
	if evt.IsError != nil && *evt.IsError {
		t.Error("expected isError to be false or nil")
	}
	if evt.ResourceName == nil || *evt.ResourceName != "greet" {
		t.Errorf("expected resource name 'greet', got %v", evt.ResourceName)
	}
	if evt.GetSessionId() == "" {
		t.Error("expected non-empty session ID")
	}
	if evt.ProjectId != "proj_test" {
		t.Errorf("expected project ID 'proj_test', got %v", evt.ProjectId)
	}

	// Check parameters
	if evt.Parameters != nil {
		if evt.Parameters["name"] != "greet" {
			t.Errorf("expected param name 'greet', got %v", evt.Parameters["name"])
		}
	}
}

func TestMiddleware_ToolCall_WithErrorResult(t *testing.T) {
	_, clientSession, mock, cleanup := createTestServer(t, nil)
	defer cleanup()

	ctx := context.Background()

	// Call the fail tool
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "fail_tool",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	// The tool returns IsError=true in its result
	if !result.IsError {
		t.Error("expected tool result to have IsError=true")
	}

	// Wait for events (init + notifications/initialized + tool call)
	events := mock.waitForEvents(3, 2*time.Second)

	var toolEvents []*agentcat.Event
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == "mcp:tools/call" {
			toolEvents = append(toolEvents, evt)
		}
	}

	if len(toolEvents) == 0 {
		t.Fatal("expected at least one tool call event")
	}

	evt := toolEvents[0]
	if evt.IsError == nil || !*evt.IsError {
		t.Error("expected isError to be true for failed tool result")
	}
	if evt.Error == nil {
		t.Error("expected error details to be set")
	} else {
		if msg, ok := evt.Error["message"].(string); ok {
			if !strings.Contains(msg, "something went wrong") {
				t.Errorf("expected error message to contain 'something went wrong', got '%s'", msg)
			}
		}
	}
}

func TestMiddleware_ToolsList_InjectsContext(t *testing.T) {
	opts := DefaultOptions()

	_, clientSession, _, cleanup := createTestServer(t, opts)
	defer cleanup()

	ctx := context.Background()

	// List tools
	var tools []*mcp.Tool
	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("Tools error: %v", err)
		}
		tools = append(tools, tool)
	}

	// Should have at least the greet and fail_tool tools
	if len(tools) < 2 {
		t.Fatalf("expected at least 2 tools, got %d", len(tools))
	}

	// Check that context param was injected into all tools
	for _, tool := range tools {
		schema := schemaToMap(tool.InputSchema)
		if schema == nil {
			t.Errorf("tool %s: expected non-nil schema", tool.Name)
			continue
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Errorf("tool %s: expected properties in schema", tool.Name)
			continue
		}
		if _, exists := props[contextParamName]; !exists {
			t.Errorf("tool %s: expected context param to be injected", tool.Name)
		}
	}
}

func TestMiddleware_ToolsList_NoContextWhenDisabled(t *testing.T) {
	opts := DefaultOptions()
	opts.DisableToolCallContext = true
	opts.DisableReportMissing = true

	_, clientSession, _, cleanup := createTestServer(t, opts)
	defer cleanup()

	ctx := context.Background()

	// List tools
	var tools []*mcp.Tool
	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("Tools error: %v", err)
		}
		tools = append(tools, tool)
	}

	// Check that context param was NOT injected
	for _, tool := range tools {
		schema := schemaToMap(tool.InputSchema)
		if schema == nil {
			continue
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			continue
		}
		if _, exists := props[contextParamName]; exists {
			t.Errorf("tool %s: context param should NOT be injected when disabled", tool.Name)
		}
	}
}

func TestMiddleware_CapturesInitializeEvent(t *testing.T) {
	_, _, mock, cleanup := createTestServer(t, nil)
	defer cleanup()

	// Wait for the initialize event that happens during connection
	events := mock.waitForEvents(1, 2*time.Second)

	var initEvents []*agentcat.Event
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == "mcp:initialize" {
			initEvents = append(initEvents, evt)
		}
	}

	if len(initEvents) == 0 {
		t.Fatal("expected at least one initialize event")
	}

	evt := initEvents[0]
	if evt.GetSessionId() == "" {
		t.Error("expected non-empty session ID")
	}
}

func TestMiddleware_WithIdentify(t *testing.T) {
	identifyCalled := false
	opts := DefaultOptions()
	opts.Identify = func(ctx context.Context, request *mcp.CallToolRequest) *agentcat.UserIdentity {
		identifyCalled = true
		return &agentcat.UserIdentity{
			UserID:   "user_123",
			UserName: "Test User",
			UserData: map[string]any{"role": "admin"},
		}
	}

	_, clientSession, mock, cleanup := createTestServer(t, opts)
	defer cleanup()

	ctx := context.Background()

	// Make a tool call to trigger identify
	_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "World"},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	// Wait for events (init + notifications/initialized + identify + tool call)
	// The identify event and tool call event are both published for the first tool call.
	events := mock.waitForEvents(4, 3*time.Second)

	if !identifyCalled {
		t.Error("expected Identify function to be called")
	}

	// Find the identify event
	var identifyEvents []*agentcat.Event
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == "mcpcat:identify" {
			identifyEvents = append(identifyEvents, evt)
		}
	}

	if len(identifyEvents) == 0 {
		t.Fatal("expected an identify event to be published")
	}

	identifyEvt := identifyEvents[0]
	if identifyEvt.IdentifyActorGivenId == nil || *identifyEvt.IdentifyActorGivenId != "user_123" {
		t.Errorf("expected identify actor ID 'user_123', got %v", identifyEvt.IdentifyActorGivenId)
	}
	if identifyEvt.IdentifyActorName == nil || *identifyEvt.IdentifyActorName != "Test User" {
		t.Errorf("expected identify actor name 'Test User', got %v", identifyEvt.IdentifyActorName)
	}
}

func TestMiddleware_IdentifyCalledOncePerSession(t *testing.T) {
	identifyCount := 0
	var mu sync.Mutex
	opts := DefaultOptions()
	opts.Identify = func(ctx context.Context, request *mcp.CallToolRequest) *agentcat.UserIdentity {
		mu.Lock()
		identifyCount++
		mu.Unlock()
		return &agentcat.UserIdentity{
			UserID:   "user_123",
			UserName: "Test User",
		}
	}

	_, clientSession, mock, cleanup := createTestServer(t, opts)
	defer cleanup()

	ctx := context.Background()

	// Make two tool calls
	_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "World"},
	})
	if err != nil {
		t.Fatalf("first CallTool error: %v", err)
	}

	// Wait for the first tool call event to be processed
	mock.waitForEvents(2, 2*time.Second) // init + first tool call

	_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "Again"},
	})
	if err != nil {
		t.Fatalf("second CallTool error: %v", err)
	}

	// Wait for events
	mock.waitForEvents(4, 2*time.Second)

	mu.Lock()
	count := identifyCount
	mu.Unlock()

	if count != 1 {
		t.Errorf("expected Identify to be called exactly once, got %d", count)
	}
}

func TestMiddleware_ToolCall_WithUserIntent(t *testing.T) {
	opts := DefaultOptions()

	_, clientSession, mock, cleanup := createTestServer(t, opts)
	defer cleanup()

	ctx := context.Background()

	// Call tool with context param
	_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "greet",
		Arguments: map[string]any{
			"name":    "World",
			"context": "Greeting the user for a welcome message",
		},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	// Wait for events (init + notifications/initialized + tool call)
	events := mock.waitForEvents(3, 2*time.Second)

	var toolEvents []*agentcat.Event
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == "mcp:tools/call" {
			toolEvents = append(toolEvents, evt)
		}
	}

	if len(toolEvents) == 0 {
		t.Fatal("expected at least one tool call event")
	}

	evt := toolEvents[0]
	if evt.UserIntent == nil {
		t.Fatal("expected UserIntent to be set")
	}
	if *evt.UserIntent != "Greeting the user for a welcome message" {
		t.Errorf("expected intent 'Greeting the user for a welcome message', got '%s'", *evt.UserIntent)
	}

	// Verify context is filtered from parameters
	if evt.Parameters != nil {
		if args, ok := evt.Parameters["arguments"].(map[string]any); ok {
			if _, hasContext := args["context"]; hasContext {
				t.Error("context should be filtered from parameters")
			}
		}
	}
}

func TestMiddleware_CapturesSessionMetadata(t *testing.T) {
	_, clientSession, mock, cleanup := createTestServer(t, nil)
	defer cleanup()

	ctx := context.Background()

	// Call tool to generate an event
	_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "World"},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	// Wait for events (init + notifications/initialized + tool call)
	events := mock.waitForEvents(3, 2*time.Second)

	var toolEvents []*agentcat.Event
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == "mcp:tools/call" {
			toolEvents = append(toolEvents, evt)
		}
	}

	if len(toolEvents) == 0 {
		t.Fatal("expected at least one tool call event")
	}

	evt := toolEvents[0]

	// Verify session metadata
	if evt.SdkLanguage == nil || *evt.SdkLanguage != "Go" {
		t.Errorf("expected SDK language 'Go', got %v", evt.SdkLanguage)
	}
	if evt.ClientName == nil || *evt.ClientName != "test-client" {
		t.Errorf("expected client name 'test-client', got %v", evt.ClientName)
	}
	if evt.ClientVersion == nil || *evt.ClientVersion != "0.1.0" {
		t.Errorf("expected client version '0.1.0', got %v", evt.ClientVersion)
	}
	if evt.ServerName == nil || *evt.ServerName != "test-server" {
		t.Errorf("expected server name 'test-server', got %v", evt.ServerName)
	}
	if evt.ServerVersion == nil || *evt.ServerVersion != "1.0.0" {
		t.Errorf("expected server version '1.0.0', got %v", evt.ServerVersion)
	}
}

func TestMiddleware_GetMoreTools_RegisteredWhenEnabled(t *testing.T) {
	opts := DefaultOptions()

	_, clientSession, _, cleanup := createTestServer(t, opts)
	defer cleanup()

	ctx := context.Background()

	// List tools and check for get_more_tools
	var found bool
	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("Tools error: %v", err)
		}
		if tool.Name == "get_more_tools" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected get_more_tools to be registered when DisableReportMissing is false")
	}
}

func TestMiddleware_GetMoreTools_NotRegisteredWhenDisabled(t *testing.T) {
	opts := DefaultOptions()
	opts.DisableReportMissing = true

	_, clientSession, _, cleanup := createTestServer(t, opts)
	defer cleanup()

	ctx := context.Background()

	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("Tools error: %v", err)
		}
		if tool.Name == "get_more_tools" {
			t.Error("get_more_tools should NOT be registered when DisableReportMissing is true")
			break
		}
	}
}

// greetArgs is used by struct-based tool registration tests to reproduce
// the additionalProperties: false behavior from the official go-sdk.
type greetArgs struct {
	Name string `json:"name" jsonschema:"Name to greet"`
}

// greetResult is the return type for the struct-based greet tool.
type greetResult struct {
	Text string `json:"text"`
}

// failToolArgs is an empty struct for the fail_tool (no arguments).
type failToolArgs struct{}

func TestMiddleware_GetMoreTools_Call(t *testing.T) {
	opts := DefaultOptions()

	_, clientSession, _, cleanup := createTestServer(t, opts)
	defer cleanup()

	ctx := context.Background()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_more_tools",
		Arguments: map[string]any{
			"context": "I need a tool that can analyze images",
		},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	if result.IsError {
		t.Error("expected get_more_tools to succeed")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content")
	}
	if tc, ok := result.Content[0].(*mcp.TextContent); ok {
		if !strings.Contains(tc.Text, "full tool list") {
			t.Errorf("expected response to mention full tool list, got '%s'", tc.Text)
		}
	}
}
