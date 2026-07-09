package officialsdk

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk"
)

// setupStdio creates a server and client connected via IO pipes (simulating
// stdio transport) with MCPCat tracking middleware installed.
func setupStdio(t *testing.T, opts *Options) (*mcp.ClientSession, *TodoStore, *mockPublisher) {
	t.Helper()

	server, store, mock := createFullTestServerWithTracking(t, opts)

	// Create bidirectional pipes: server reads from clientWriter, writes to clientReader.
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()

	serverTransport := &mcp.IOTransport{Reader: serverReader, Writer: serverWriter}
	clientTransport := &mcp.IOTransport{Reader: clientReader, Writer: clientWriter}

	ctx := context.Background()

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}

	t.Cleanup(func() {
		clientSession.Close()
		serverSession.Wait()
	})

	return clientSession, store, mock
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

func TestStdio_ToolCall_FullPipeline(t *testing.T) {
	clientSession, _, mock := setupStdio(t, nil)
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

	// Wait for events: init + notifications/initialized + tool call
	events := mock.waitForEvents(3, 3*time.Second)
	toolEvents := filterEvents(events, "mcp:tools/call")

	if len(toolEvents) == 0 {
		t.Fatal("expected at least one mcp:tools/call event")
	}

	evt := toolEvents[0]

	// Verify event type
	if evt.EventType == nil || *evt.EventType != "mcp:tools/call" {
		t.Errorf("expected event type 'mcp:tools/call', got %v", evt.EventType)
	}

	// Verify duration is non-negative
	if evt.Duration == nil || *evt.Duration < 0 {
		t.Error("expected non-negative duration")
	}

	// Verify no error
	if evt.IsError != nil && *evt.IsError {
		t.Error("expected isError to be false or nil")
	}

	// Verify resource name is the tool name
	if evt.ResourceName == nil || *evt.ResourceName != "add_todo" {
		t.Errorf("expected resource name 'add_todo', got %v", evt.ResourceName)
	}

	// Verify session ID
	if evt.GetSessionId() == "" {
		t.Error("expected non-empty session ID")
	}

	// Verify project ID
	if evt.ProjectId != "proj_test" {
		t.Errorf("expected project ID 'proj_test', got %v", evt.ProjectId)
	}

	// Verify parameters contain the tool name
	if evt.Parameters != nil {
		if evt.Parameters["name"] != "add_todo" {
			t.Errorf("expected param name 'add_todo', got %v", evt.Parameters["name"])
		}
	} else {
		t.Error("expected non-nil parameters")
	}

	// Verify response is captured
	if evt.Response == nil {
		t.Error("expected non-nil response")
	}
}

func TestStdio_ErrorToolCall(t *testing.T) {
	clientSession, _, mock := setupStdio(t, nil)
	ctx := context.Background()

	// Call get_todo with a non-existent ID to trigger an error
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_todo",
		Arguments: map[string]any{"id": float64(9999)},
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

func TestStdio_IdentifyInvoked(t *testing.T) {
	identifyCalled := false
	opts := DefaultOptions()
	opts.Identify = func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
		identifyCalled = true
		return &agentcat.UserIdentity{
			UserID:   "user_456",
			UserName: "E2E User",
			UserData: map[string]any{"plan": "pro"},
		}
	}

	clientSession, _, mock := setupStdio(t, opts)
	ctx := context.Background()

	_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "Test identify"},
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

	if evt.IdentifyActorGivenId == nil || *evt.IdentifyActorGivenId != "user_456" {
		t.Errorf("expected identify actor ID 'user_456', got %v", evt.IdentifyActorGivenId)
	}
	if evt.IdentifyActorName == nil || *evt.IdentifyActorName != "E2E User" {
		t.Errorf("expected identify actor name 'E2E User', got %v", evt.IdentifyActorName)
	}
}

func TestStdio_SessionMetadata(t *testing.T) {
	clientSession, _, mock := setupStdio(t, nil)
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
	if evt.ClientName == nil || *evt.ClientName != "stdio-test-client" {
		t.Errorf("expected client name 'stdio-test-client', got %v", evt.ClientName)
	}
	if evt.ClientVersion == nil || *evt.ClientVersion != "1.0.0" {
		t.Errorf("expected client version '1.0.0', got %v", evt.ClientVersion)
	}
	if evt.ServerName == nil || *evt.ServerName != "todo-test-server" {
		t.Errorf("expected server name 'todo-test-server', got %v", evt.ServerName)
	}
	if evt.ServerVersion == nil || *evt.ServerVersion != "1.0.0" {
		t.Errorf("expected server version '1.0.0', got %v", evt.ServerVersion)
	}
}

func TestStdio_ResourceRead(t *testing.T) {
	clientSession, store, mock := setupStdio(t, nil)
	ctx := context.Background()

	// Add a todo so the resource has data
	store.add("Resource test todo")

	result, err := clientSession.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: "todo://list",
	})
	if err != nil {
		t.Fatalf("ReadResource error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected non-empty resource contents")
	}
	if !strings.Contains(result.Contents[0].Text, "Resource test todo") {
		t.Errorf("expected resource contents to contain 'Resource test todo', got %s", result.Contents[0].Text)
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

func TestStdio_PromptGet(t *testing.T) {
	clientSession, _, mock := setupStdio(t, nil)
	ctx := context.Background()

	result, err := clientSession.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "summarize_todos",
		Arguments: map[string]string{"style": "brief"},
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
	// Verify parameters contain the prompt name
	if evt.Parameters != nil {
		if evt.Parameters["name"] != "summarize_todos" {
			t.Errorf("expected param name 'summarize_todos', got %v", evt.Parameters["name"])
		}
	} else {
		t.Error("expected non-nil parameters")
	}
}

func TestStdio_UserIntent(t *testing.T) {
	opts := DefaultOptions()

	clientSession, _, mock := setupStdio(t, opts)
	ctx := context.Background()

	_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "add_todo",
		Arguments: map[string]any{
			"title":   "Intent test todo",
			"context": "Adding a todo to test user intent tracking through the pipeline",
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
	if *evt.UserIntent != "Adding a todo to test user intent tracking through the pipeline" {
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

func TestStdio_GetMoreTools(t *testing.T) {
	opts := DefaultOptions()

	clientSession, _, mock := setupStdio(t, opts)
	ctx := context.Background()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "get_more_tools",
		Arguments: map[string]any{
			"context": "I need a tool that can send emails",
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
