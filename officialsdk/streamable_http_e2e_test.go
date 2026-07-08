package officialsdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk"
)

// setupStreamableHTTP creates a todo server with MCPCat tracking, exposes it
// over an httptest server using StreamableHTTP transport, and returns a
// connected client session along with the backing TodoStore and mockPublisher.
func setupStreamableHTTP(t *testing.T, opts *Options) (*mcp.ClientSession, *TodoStore, *mockPublisher) {
	t.Helper()

	server, store, mock := createFullTestServerWithTracking(t, opts)

	handler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{JSONResponse: true},
	)

	httpServer := httptest.NewServer(handler)

	transport := &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL,
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "http-test-client", Version: "2.0.0"}, nil)
	clientSession, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		httpServer.Close()
		t.Fatalf("client.Connect: %v", err)
	}

	t.Cleanup(func() {
		clientSession.Close()
		httpServer.Close()
	})

	return clientSession, store, mock
}

func TestHTTP_ToolCall_FullPipeline(t *testing.T) {
	clientSession, _, mock := setupStreamableHTTP(t, nil)
	ctx := context.Background()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "Buy groceries"},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected tool call to succeed")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty content")
	}

	events := mock.waitForEvents(3, 3*time.Second)
	toolEvents := filterEvents(events, "mcp:tools/call")

	if len(toolEvents) == 0 {
		t.Fatal("expected at least one mcp:tools/call event")
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
	if evt.ResourceName == nil || *evt.ResourceName != "add_todo" {
		t.Errorf("expected resource name 'add_todo', got %v", evt.ResourceName)
	}
	if evt.GetSessionId() == "" {
		t.Error("expected non-empty session ID")
	}
	if evt.ProjectId != "proj_test" {
		t.Errorf("expected project ID 'proj_test', got %v", evt.ProjectId)
	}
}

func TestHTTP_ErrorToolCall(t *testing.T) {
	clientSession, _, mock := setupStreamableHTTP(t, nil)
	ctx := context.Background()

	// Call get_todo with a non-existent ID to trigger an error
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_todo",
		Arguments: map[string]any{"id": float64(999)},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool result to have IsError=true")
	}

	events := mock.waitForEvents(3, 3*time.Second)
	toolEvents := filterEvents(events, "mcp:tools/call")

	if len(toolEvents) == 0 {
		t.Fatal("expected at least one mcp:tools/call event")
	}

	evt := toolEvents[0]

	if evt.IsError == nil || !*evt.IsError {
		t.Error("expected isError to be true for failed tool result")
	}
	if evt.Error == nil {
		t.Error("expected error details to be set")
	} else {
		if msg, ok := evt.Error["message"].(string); ok {
			if !strings.Contains(msg, "not found") {
				t.Errorf("expected error message to contain 'not found', got '%s'", msg)
			}
		} else {
			t.Error("expected error message to be a string")
		}
	}
}

func TestHTTP_IdentifyInvoked(t *testing.T) {
	identifyCalled := false
	opts := DefaultOptions()
	opts.Identify = func(ctx context.Context, request *mcp.CallToolRequest) *agentcat.UserIdentity {
		identifyCalled = true
		return &agentcat.UserIdentity{
			UserID:   "http_user_789",
			UserName: "HTTP E2E User",
			UserData: map[string]any{"plan": "enterprise"},
		}
	}

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "Test identify via HTTP"},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	// Wait for events: init + notifications/initialized + identify + tool call
	events := mock.waitForEvents(4, 3*time.Second)

	if !identifyCalled {
		t.Error("expected Identify function to be called")
	}

	identifyEvents := filterEvents(events, "agentcat:identify")
	if len(identifyEvents) == 0 {
		t.Fatal("expected an agentcat:identify event to be published")
	}

	evt := identifyEvents[0]

	if evt.IdentifyActorGivenId == nil || *evt.IdentifyActorGivenId != "http_user_789" {
		t.Errorf("expected identify actor ID 'http_user_789', got %v", evt.IdentifyActorGivenId)
	}
	if evt.IdentifyActorName == nil || *evt.IdentifyActorName != "HTTP E2E User" {
		t.Errorf("expected identify actor name 'HTTP E2E User', got %v", evt.IdentifyActorName)
	}
}

// TestHTTP_IdentifyRerunAndEventDedup verifies the identify callback re-runs
// on every tool call (matching the TypeScript SDK) while the agentcat:identify
// event is published only when the identity changes.
func TestHTTP_IdentifyRerunAndEventDedup(t *testing.T) {
	var mu sync.Mutex
	identifyCount := 0
	opts := DefaultOptions()
	opts.Identify = func(ctx context.Context, request *mcp.CallToolRequest) *agentcat.UserIdentity {
		mu.Lock()
		identifyCount++
		mu.Unlock()
		return &agentcat.UserIdentity{
			UserID:   "dedup_user",
			UserName: "Dedup User",
		}
	}

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	// First tool call
	_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "First todo"},
	})
	if err != nil {
		t.Fatalf("first CallTool error: %v", err)
	}

	// Wait for the first tool call event to be processed
	mock.waitForEvents(3, 3*time.Second)

	// Second tool call in the same session
	_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "Second todo"},
	})
	if err != nil {
		t.Fatalf("second CallTool error: %v", err)
	}

	// Wait for second tool call events
	events := mock.waitForEvents(5, 3*time.Second)

	mu.Lock()
	count := identifyCount
	mu.Unlock()

	// The callback re-runs on every tool call so identity changes can be
	// detected.
	if count != 2 {
		t.Errorf("expected Identify to be called on each tool call (2), got %d", count)
	}

	// The identify event is deduplicated: the identity never changed, so
	// exactly one agentcat:identify event is published.
	identifyEvents := 0
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == "agentcat:identify" {
			identifyEvents++
		}
	}
	if identifyEvents != 1 {
		t.Errorf("expected exactly 1 identify event (identity unchanged), got %d", identifyEvents)
	}
}

func TestHTTP_SessionMetadata(t *testing.T) {
	clientSession, _, mock := setupStreamableHTTP(t, nil)
	ctx := context.Background()

	_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_todos",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	events := mock.waitForEvents(3, 3*time.Second)
	toolEvents := filterEvents(events, "mcp:tools/call")

	if len(toolEvents) == 0 {
		t.Fatal("expected at least one mcp:tools/call event")
	}

	evt := toolEvents[0]

	if evt.SdkLanguage == nil || *evt.SdkLanguage != "Go" {
		t.Errorf("expected SDK language 'Go', got %v", evt.SdkLanguage)
	}
	if evt.ClientName == nil || *evt.ClientName != "http-test-client" {
		t.Errorf("expected client name 'http-test-client', got %v", evt.ClientName)
	}
	if evt.ClientVersion == nil || *evt.ClientVersion != "2.0.0" {
		t.Errorf("expected client version '2.0.0', got %v", evt.ClientVersion)
	}
	if evt.ServerName == nil || *evt.ServerName != "todo-test-server" {
		t.Errorf("expected server name 'todo-test-server', got %v", evt.ServerName)
	}
	if evt.ServerVersion == nil || *evt.ServerVersion != "1.0.0" {
		t.Errorf("expected server version '1.0.0', got %v", evt.ServerVersion)
	}
}

func TestHTTP_ResourceRead(t *testing.T) {
	clientSession, store, mock := setupStreamableHTTP(t, nil)
	ctx := context.Background()

	// Add a todo so the resource has data
	store.add("HTTP resource test todo")

	result, err := clientSession.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "todo://list",
	})
	if err != nil {
		t.Fatalf("ReadResource error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected non-empty resource contents")
	}
	if !strings.Contains(result.Contents[0].Text, "HTTP resource test todo") {
		t.Errorf("expected resource contents to contain 'HTTP resource test todo', got %s", result.Contents[0].Text)
	}

	events := mock.waitForEvents(3, 3*time.Second)
	resourceEvents := filterEvents(events, "mcp:resources/read")

	if len(resourceEvents) == 0 {
		t.Fatal("expected at least one mcp:resources/read event")
	}

	evt := resourceEvents[0]

	if evt.EventType == nil || *evt.EventType != "mcp:resources/read" {
		t.Errorf("expected event type 'mcp:resources/read', got %v", evt.EventType)
	}
	if evt.ResourceName == nil || *evt.ResourceName != "todo://list" {
		t.Errorf("expected resource name 'todo://list', got %v", evt.ResourceName)
	}
	if evt.Duration == nil || *evt.Duration < 0 {
		t.Error("expected non-negative duration")
	}
	if evt.GetSessionId() == "" {
		t.Error("expected non-empty session ID")
	}
}

func TestHTTP_PromptGet(t *testing.T) {
	clientSession, _, mock := setupStreamableHTTP(t, nil)
	ctx := context.Background()

	result, err := clientSession.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "summarize_todos",
		Arguments: map[string]string{"style": "detailed"},
	})
	if err != nil {
		t.Fatalf("GetPrompt error: %v", err)
	}
	if result.Description != "Todo list summary prompt" {
		t.Errorf("expected description 'Todo list summary prompt', got '%s'", result.Description)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected non-empty messages")
	}

	events := mock.waitForEvents(3, 3*time.Second)
	promptEvents := filterEvents(events, "mcp:prompts/get")

	if len(promptEvents) == 0 {
		t.Fatal("expected at least one mcp:prompts/get event")
	}

	evt := promptEvents[0]

	if evt.EventType == nil || *evt.EventType != "mcp:prompts/get" {
		t.Errorf("expected event type 'mcp:prompts/get', got %v", evt.EventType)
	}
	if evt.Duration == nil || *evt.Duration < 0 {
		t.Error("expected non-negative duration")
	}
	if evt.GetSessionId() == "" {
		t.Error("expected non-empty session ID")
	}
	if evt.Parameters != nil {
		if evt.Parameters["name"] != "summarize_todos" {
			t.Errorf("expected param name 'summarize_todos', got %v", evt.Parameters["name"])
		}
	} else {
		t.Error("expected non-nil parameters")
	}
}

func TestHTTP_UserIntent(t *testing.T) {
	opts := DefaultOptions()

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "add_todo",
		Arguments: map[string]any{
			"title":   "Intent test todo",
			"context": "Adding a todo to verify user intent tracking over HTTP transport",
		},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	events := mock.waitForEvents(3, 3*time.Second)
	toolEvents := filterEvents(events, "mcp:tools/call")

	if len(toolEvents) == 0 {
		t.Fatal("expected at least one mcp:tools/call event")
	}

	evt := toolEvents[0]

	if evt.UserIntent == nil {
		t.Fatal("expected UserIntent to be set")
	}
	if *evt.UserIntent != "Adding a todo to verify user intent tracking over HTTP transport" {
		t.Errorf("expected specific intent string, got '%s'", *evt.UserIntent)
	}

	// Verify context is filtered from parameters
	if evt.Parameters != nil {
		if args, ok := evt.Parameters["arguments"].(map[string]any); ok {
			if _, hasContext := args["context"]; hasContext {
				t.Error("context should be filtered from parameters")
			}
			// The title argument should still be present
			if _, hasTitle := args["title"]; !hasTitle {
				t.Error("expected 'title' argument to remain in parameters")
			}
		}
	}
}

func TestHTTP_GetMoreTools(t *testing.T) {
	opts := DefaultOptions()

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_more_tools",
		Arguments: map[string]any{
			"context": "I need a tool that can process images",
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

	events := mock.waitForEvents(3, 3*time.Second)
	toolEvents := filterEvents(events, "mcp:tools/call")

	if len(toolEvents) == 0 {
		t.Fatal("expected at least one mcp:tools/call event for get_more_tools")
	}

	// Find the get_more_tools event
	var getMoreToolsEvt *agentcat.Event
	for _, evt := range toolEvents {
		if evt.ResourceName != nil && *evt.ResourceName == "get_more_tools" {
			getMoreToolsEvt = evt
			break
		}
	}

	if getMoreToolsEvt == nil {
		t.Fatal("expected a tool call event with resource name 'get_more_tools'")
	}

	if getMoreToolsEvt.EventType == nil || *getMoreToolsEvt.EventType != "mcp:tools/call" {
		t.Errorf("expected event type 'mcp:tools/call', got %v", getMoreToolsEvt.EventType)
	}
	if getMoreToolsEvt.GetSessionId() == "" {
		t.Error("expected non-empty session ID")
	}
}

func TestHTTP_ExtraDataCaptured(t *testing.T) {
	clientSession, _, mock := setupStreamableHTTP(t, nil)
	ctx := context.Background()

	_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "Test extra data"},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	events := mock.waitForEvents(3, 3*time.Second)
	toolEvents := filterEvents(events, "mcp:tools/call")

	if len(toolEvents) == 0 {
		t.Fatal("expected at least one mcp:tools/call event")
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

func TestHTTP_ContextParamInjection(t *testing.T) {
	opts := DefaultOptions()

	clientSession, _, _ := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	var tools []*mcp.Tool
	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("Tools error: %v", err)
		}
		tools = append(tools, tool)
	}

	if len(tools) == 0 {
		t.Fatal("expected at least one tool")
	}

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
