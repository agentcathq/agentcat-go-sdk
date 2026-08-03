package officialsdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// setupStreamableHTTP creates a todo server with MCPCat tracking, exposes it
// over an httptest server using StreamableHTTP transport, and returns a
// connected client session along with the backing TodoStore and mockPublisher.
//
// This is the STATEFUL shape: one long-lived server behind a session-bearing
// handler. See setupStreamableHTTPWith for the stateless variants.
func setupStreamableHTTP(t *testing.T, opts *Options) (*mcp.ClientSession, *TodoStore, *mockPublisher) {
	t.Helper()
	return setupStreamableHTTPWith(t, opts, &mcp.StreamableHTTPOptions{JSONResponse: true}, false)
}

// setupStreamableHTTPWith is setupStreamableHTTP with explicit transport
// options and an optional pre-2026 client. One server instance serves every
// request, so in stateless mode the instance is long-lived while the session
// is not — the shape a customer gets from a single mcp.NewServer behind a
// sessionless deployment. (The per-request factory shape is covered in
// factory_e2e_test.go.)
func setupStreamableHTTPWith(t *testing.T, opts *Options, httpOpts *mcp.StreamableHTTPOptions, legacyClient bool) (*mcp.ClientSession, *TodoStore, *mockPublisher) {
	t.Helper()

	server, store, mock := createFullTestServerWithTracking(t, opts)

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return server }, httpOpts)
	httpServer := httptest.NewServer(handler)

	transport := &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL,
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "http-test-client", Version: "2.0.0"}, nil)
	if legacyClient {
		pinLegacyInitializeHandshake(client)
	}
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

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
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
	if !strings.HasPrefix(evt.GetSessionId(), "ses_") {
		t.Errorf("expected a minted ses_ session ID, got %q", evt.GetSessionId())
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

func TestHTTP_IdentifyInvoked(t *testing.T) {
	// The callback runs once per tool call, on that call's goroutine,
	// so the flag must be synchronized with the test goroutine.
	var identifyCalled atomic.Bool
	opts := DefaultOptions()
	opts.Identify = func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
		identifyCalled.Store(true)
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
	if evt.IdentifyActorGivenId == nil || *evt.IdentifyActorGivenId != "http_user_789" {
		t.Errorf("expected identify actor ID 'http_user_789' on the event, got %v", evt.IdentifyActorGivenId)
	}
	if evt.IdentifyActorName == nil || *evt.IdentifyActorName != "HTTP E2E User" {
		t.Errorf("expected identify actor name 'HTTP E2E User' on the event, got %v", evt.IdentifyActorName)
	}
	if evt.IdentifyData["plan"] != "enterprise" {
		t.Errorf("expected identify data stamped on the event, got %v", evt.IdentifyData)
	}

	if identifies := filterEvents(mock.getEvents(), "agentcat:identify"); len(identifies) != 0 {
		t.Errorf("v2 publishes no agentcat:identify events, got %d", len(identifies))
	}
}

// TestHTTP_IdentifyStampsEveryToolCall verifies the identify callback re-runs
// on every tool call and stamps each call's event — even when the identity is
// unchanged (no change-detection dedup).
func TestHTTP_IdentifyStampsEveryToolCall(t *testing.T) {
	var mu sync.Mutex
	identifyCount := 0
	opts := DefaultOptions()
	opts.Identify = func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
		mu.Lock()
		identifyCount++
		mu.Unlock()
		return &agentcat.UserIdentity{
			UserID:   "rerun_user",
			UserName: "Rerun User",
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
	waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)

	// Second tool call in the same session
	_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "Second todo"},
	})
	if err != nil {
		t.Fatalf("second CallTool error: %v", err)
	}

	toolEvents := waitForEventType(mock, "mcp:tools/call", 2, 3*time.Second)

	mu.Lock()
	count := identifyCount
	mu.Unlock()

	// The callback re-runs on every tool call.
	if count != 2 {
		t.Errorf("expected Identify to be called on each tool call (2), got %d", count)
	}

	// Every tool-call event is stamped with the returned identity.
	if len(toolEvents) != 2 {
		t.Fatalf("expected 2 tool-call events, got %d", len(toolEvents))
	}
	for i, evt := range toolEvents {
		if evt.IdentifyActorGivenId == nil || *evt.IdentifyActorGivenId != "rerun_user" {
			t.Errorf("event %d not stamped with identity: %v", i, evt.IdentifyActorGivenId)
		}
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

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(toolEvents) == 0 {
		t.Fatal("expected at least one mcp:tools/call event")
	}

	evt := toolEvents[0]

	// The 2026-protocol client sends identity in per-request _meta; the
	// event must carry it either way (the accessor falls back to the legacy
	// initialize capture for older clients).
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

// TestHTTP_ResourceRead verifies resource reads pass through untouched and —
// per the v2 design — publish no event.
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

	settleForAbsentEvents()
	if events := mock.getEvents(); len(events) != 0 {
		t.Errorf("v2 publishes no resources/read events, got %d event(s)", len(events))
	}
}

// TestHTTP_PromptGet verifies prompt requests pass through untouched and —
// per the v2 design — publish no event.
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

	settleForAbsentEvents()
	if events := mock.getEvents(); len(events) != 0 {
		t.Errorf("v2 publishes no prompts/get events, got %d event(s)", len(events))
	}
}

// TestHTTP_FullSessionPublishesOnlyToolCalls drives the whole MCP surface the
// fixture exposes over a real HTTP transport and pins the v2 rule: tool calls
// are the only thing that publishes.
func TestHTTP_FullSessionPublishesOnlyToolCalls(t *testing.T) {
	clientSession, store, mock := setupStreamableHTTP(t, nil)
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

// TestHTTP_SuppliedSessionRoundTrips pins that a session_id supplied by the agent
// survives a real HTTP hop: every call carrying it is attributed to it, and
// none of them re-mints.
func TestHTTP_SuppliedSessionRoundTrips(t *testing.T) {
	clientSession, _, mock := setupStreamableHTTP(t, nil)
	ctx := context.Background()

	session := sid("http_supplied")
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

// TestHTTP_StatelessSharedServer covers the sessionless HTTP deployment of a
// single long-lived server: no Mcp-Session-Id, a fresh temporary session per
// request, and every tool call still resolved, minted, and published.
func TestHTTP_StatelessSharedServer(t *testing.T) {
	clientSession, _, mock := setupStreamableHTTPWith(t, nil,
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}, false)
	ctx := context.Background()

	res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "stateless", "context": "why"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("stateless call failed: %v", res.Content)
	}

	events := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 mcp:tools/call event in stateless mode, got %d", len(events))
	}
	evt := events[0]
	minted := evt.GetSessionId()
	if !strings.HasPrefix(minted, "ses_") {
		t.Errorf("expected a minted ses_ session, got %q", minted)
	}
	if got := lastText(t, res); got != mintBackFor(minted) {
		t.Errorf("stateless mint-back = %q, want %q", got, mintBackFor(minted))
	}
	if evt.UserIntent == nil || *evt.UserIntent != "why" {
		t.Errorf("user intent = %v, want %q", evt.UserIntent, "why")
	}

	// A second call supplying that session is attributed to it — correlation
	// survives with no transport session to carry it.
	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "stateless 2", "session_id": minted},
	}); err != nil {
		t.Fatalf("second CallTool: %v", err)
	}
	events = waitForEventType(mock, "mcp:tools/call", 2, 3*time.Second)
	if len(events) != 2 {
		t.Fatalf("expected 2 tool-call events, got %d", len(events))
	}
	// Calls may overlap, so the second call's event is selected by the title
	// it carried — never by arrival order.
	second := eventByToolArg(t, events, "title", "stateless 2")
	if second.GetSessionId() != minted {
		t.Errorf("second stateless call attributed to %q, want %q", second.GetSessionId(), minted)
	}
}

// eventByToolArg returns the one event whose recorded (raw) arguments have
// key=value, failing if there is not exactly one. Selecting an event by what
// the agent sent is order-independent; indexing into mock.events is not,
// because concurrent tool calls capture on their own goroutines.
func eventByToolArg(t *testing.T, events []*agentcat.Event, key, value string) *agentcat.Event {
	t.Helper()
	var found *agentcat.Event
	n := 0
	for _, evt := range events {
		args, _ := evt.Parameters["arguments"].(map[string]any)
		if args[key] == value {
			found = evt
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 event with argument %s=%q, got %d", key, value, n)
	}
	return found
}

// TestHTTP_StatefulLegacyClient covers the pre-2026 client over stateful HTTP:
// client identity comes from the initialize handshake rather than per-request
// _meta, and the call path behaves identically.
func TestHTTP_StatefulLegacyClient(t *testing.T) {
	clientSession, _, mock := setupStreamableHTTPWith(t, nil,
		&mcp.StreamableHTTPOptions{JSONResponse: true}, true)
	ctx := context.Background()

	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "legacy client", "session_id": sid("http_legacy")},
	}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	events := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	settleForAbsentEvents()
	if got := len(mock.getEvents()); got != 1 {
		t.Fatalf("expected exactly 1 event, got %d (%v)", got, eventTypes(mock.getEvents()))
	}

	evt := events[0]
	if evt.ClientName == nil || *evt.ClientName != "http-test-client" {
		t.Errorf("legacy client identity lost: client name = %v", evt.ClientName)
	}
	if evt.ClientVersion == nil || *evt.ClientVersion != "2.0.0" {
		t.Errorf("legacy client identity lost: client version = %v", evt.ClientVersion)
	}
	if evt.GetSessionId() != sid("http_legacy") {
		t.Errorf("event attributed to %q, want ses_http_legacy", evt.GetSessionId())
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

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
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

	// v2 records the raw request: context and title both appear verbatim.
	if args, ok := evt.Parameters["arguments"].(map[string]any); ok {
		if args["context"] != "Adding a todo to verify user intent tracking over HTTP transport" {
			t.Errorf("raw arguments must include context verbatim, got %v", args)
		}
		if _, hasTitle := args["title"]; !hasTitle {
			t.Error("expected 'title' argument to remain in parameters")
		}
	} else {
		t.Errorf("expected recorded arguments map, got %v", evt.Parameters)
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

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
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
		schema := marshalToMap(t, tool.InputSchema)
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Errorf("tool %s: expected properties in schema", tool.Name)
			continue
		}
		if _, exists := props["context"]; !exists {
			t.Errorf("tool %s: expected context param to be injected", tool.Name)
		}
	}
}
