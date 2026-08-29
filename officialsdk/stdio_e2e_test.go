package officialsdk

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
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
	// go-sdk v1.7.0 clients default to protocol 2026-07-28, whose
	// server/discover probe succeeds over pipes and skips initialize; pin the
	// legacy handshake so this suite exercises the initialize-fallback path
	// for per-request client identity.
	pinLegacyInitializeHandshake(client)
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

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
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

	// Verify a session was minted for the call
	if !strings.HasPrefix(evt.GetSessionId(), "ses_") {
		t.Errorf("expected a minted ses_ session ID, got %q", evt.GetSessionId())
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

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
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
	// The callback runs once per tool call, on that call's goroutine,
	// so the flag must be synchronized with the test goroutine.
	var identifyCalled atomic.Bool
	opts := DefaultOptions()
	opts.Identify = func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
		identifyCalled.Store(true)
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

	// v2: the identity stamps the tool-call event; there is no separate
	// agentcat:identify event.
	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(toolEvents) == 0 {
		t.Fatal("expected an mcp:tools/call event")
	}

	if !identifyCalled.Load() {
		t.Error("expected Identify function to be called")
	}

	evt := toolEvents[0]
	if evt.IdentifyActorGivenId == nil || *evt.IdentifyActorGivenId != "user_456" {
		t.Errorf("expected identify actor ID 'user_456' on the event, got %v", evt.IdentifyActorGivenId)
	}
	if evt.IdentifyActorName == nil || *evt.IdentifyActorName != "E2E User" {
		t.Errorf("expected identify actor name 'E2E User' on the event, got %v", evt.IdentifyActorName)
	}
	if evt.IdentifyData["plan"] != "pro" {
		t.Errorf("expected identify data stamped on the event, got %v", evt.IdentifyData)
	}

	if identifies := filterEvents(mock.getEvents(), "agentcat:identify"); len(identifies) != 0 {
		t.Errorf("v2 publishes no agentcat:identify events, got %d", len(identifies))
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

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(toolEvents) == 0 {
		t.Fatal("expected at least one mcp:tools/call event")
	}

	evt := toolEvents[0]

	// This suite pins the legacy handshake: client identity must come from
	// the initialize-params fallback of the per-request accessors.
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

// TestStdio_ResourceRead verifies resource reads pass through untouched and —
// per the v2 design — publish no event.
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

	settleForAbsentEvents()
	if events := mock.getEvents(); len(events) != 0 {
		t.Errorf("v2 publishes no resources/read events, got %d event(s)", len(events))
	}
}

// TestStdio_PromptGet verifies prompt requests pass through untouched and —
// per the v2 design — publish no event.
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

	settleForAbsentEvents()
	if events := mock.getEvents(); len(events) != 0 {
		t.Errorf("v2 publishes no prompts/get events, got %d event(s)", len(events))
	}
}

// TestStdio_FullSessionPublishesOnlyToolCalls drives the whole MCP surface the
// fixture exposes over a real stdio transport and pins the v2 rule: tool calls
// are the only thing that publishes.
func TestStdio_FullSessionPublishesOnlyToolCalls(t *testing.T) {
	clientSession, store, mock := setupStdio(t, nil)
	ctx := context.Background()
	store.add("seed")

	if _, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if _, err := clientSession.ReadResource(ctx, &mcp.ReadResourceParams{URI: "todo://list"}); err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if _, err := clientSession.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "summarize_todos", Arguments: map[string]string{"style": "brief"},
	}); err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	for _, title := range []string{"one", "two"} {
		if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name: "add_todo", Arguments: map[string]any{"title": title},
		}); err != nil {
			t.Fatalf("CallTool %s: %v", title, err)
		}
	}

	waitForEventType(mock, "mcp:tools/call", 2, 3*time.Second)
	settleForAbsentEvents()

	events := mock.getEvents()
	for _, e := range events {
		if e.EventType == nil || *e.EventType != "mcp:tools/call" {
			t.Errorf("unexpected event type %v — v2 publishes only tool calls", e.EventType)
		}
	}
	if got := len(events); got != 2 {
		t.Errorf("expected exactly 2 events for 2 tool calls, got %d (%v)", got, eventTypes(events))
	}
}

// TestStdio_SuppliedSessionRoundTrips pins that a session_id supplied by the agent
// survives a real stdio hop: every call carrying it is attributed to it, and
// none of them re-mints.
func TestStdio_SuppliedSessionRoundTrips(t *testing.T) {
	clientSession, _, mock := setupStdio(t, nil)
	ctx := context.Background()

	session := sid("stdio_supplied")
	for _, title := range []string{"first", "second"} {
		res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name:      "add_todo",
			Arguments: map[string]any{"title": title, "session_id": session},
		})
		if err != nil {
			t.Fatalf("CallTool %s: %v", title, err)
		}
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "session_id issued") {
				t.Errorf("call %s re-minted a session the agent already supplied", title)
			}
		}
	}

	events := waitForEventType(mock, "mcp:tools/call", 2, 3*time.Second)
	if len(events) != 2 {
		t.Fatalf("expected 2 tool-call events, got %d", len(events))
	}
	for i, evt := range events {
		if evt.GetSessionId() != session {
			t.Errorf("event %d attributed to %q, want %q", i, evt.GetSessionId(), session)
		}
		if evt.Tags == nil || (*evt.Tags)[agentcat.TagSessionSource] != string(agentcat.SessionSourceSupplied) {
			t.Errorf("event %d session source tag = %v, want %q", i, evt.Tags, agentcat.SessionSourceSupplied)
		}
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

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
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

	// v2 records the raw request: context and title both appear verbatim.
	if args, ok := evt.Parameters["arguments"].(map[string]any); ok {
		if args["context"] != "Adding a todo to test user intent tracking through the pipeline" {
			t.Errorf("raw arguments must include context verbatim, got %v", args)
		}
		if _, hasTitle := args["title"]; !hasTitle {
			t.Error("expected 'title' argument to remain in parameters")
		}
	} else {
		t.Errorf("expected recorded arguments map, got %v", evt.Parameters)
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
	if tc, ok := result.Content[len(result.Content)-1].(*mcp.TextContent); ok {
		if !strings.Contains(tc.Text, "full tool list") {
			t.Errorf("expected response to mention full tool list, got '%s'", tc.Text)
		}
	}

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
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
	if !strings.HasPrefix(getMoreToolsEvt.GetSessionId(), "ses_") {
		t.Errorf("expected a minted ses_ session ID, got %q", getMoreToolsEvt.GetSessionId())
	}
}
