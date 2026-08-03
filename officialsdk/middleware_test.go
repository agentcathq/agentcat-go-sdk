package officialsdk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// pinLegacyInitializeHandshake forces a test client to perform the legacy
// initialize handshake. go-sdk v1.7.0 defaults new clients to MCP protocol
// 2026-07-28 (SEP-2575), which replaces initialize with a server/discover
// probe — on in-process transports the probe succeeds, so the server never
// receives an initialize request and the middleware captures no
// mcp:initialize event. Failing the probe client-side makes Client.Connect
// fall back to the pre-2026 initialize handshake these tests were written
// against.
func pinLegacyInitializeHandshake(client *mcp.Client) {
	client.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "server/discover" {
				return nil, errors.New("test client pinned to pre-2026 protocol")
			}
			return next(ctx, method, req)
		}
	})
}

// The two settle windows a test may use to prove that NO event arrives.
// Absence has no signal to wait on, so it can only be observed by giving a
// stray event time to land. These are the only two such numbers in the package.
const (
	// settleWindow covers capture → mock publisher: a synchronous call and
	// a direct function call.
	settleWindow = 200 * time.Millisecond

	// settlePublishWindow covers capture → the real publisher's worker pool →
	// an HTTP POST to a local capture API. A strictly longer path than
	// settleWindow, so it gets a strictly longer window.
	settlePublishWindow = 500 * time.Millisecond
)

// settleForAbsentEvents waits out settleWindow. Use it only where the assertion
// is about the ABSENCE of an event; every other wait must key off a signal.
func settleForAbsentEvents() { time.Sleep(settleWindow) }

// settleForAbsentPublishes is settleForAbsentEvents for tests that assert
// against the real publisher's HTTP output rather than a mock publisher.
func settleForAbsentPublishes() { time.Sleep(settlePublishWindow) }

// eventTypes lists the event types of a slice, for failure messages.
func eventTypes(events []*agentcat.Event) []string {
	types := make([]string, 0, len(events))
	for _, evt := range events {
		if evt.EventType != nil {
			types = append(types, *evt.EventType)
		} else {
			types = append(types, "<nil>")
		}
	}
	return types
}

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
		DisableTracing:             opts.DisableTracing,
		CustomContextDescription:   opts.CustomContextDescription,
		EnableAgentTracking:        opts.EnableAgentTracking,
		Debug:                      opts.Debug,
		RedactSensitiveInformation: opts.RedactSensitiveInformation,
	}
	instance := &agentcat.AgentCatInstance{
		ProjectID: projectID,
		Options:   coreOpts,
	}
	agentcat.RegisterServer(server, instance)

	middleware := newTrackingMiddleware(server, projectID, opts, mock.publish, serverImpl)
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
	pinLegacyInitializeHandshake(client)
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
		// (The v2 mint-back block is appended AFTER the customer content.)
		if tc.Text != `{"text":"Hello, World!"}` {
			t.Errorf("expected '{\"text\":\"Hello, World!\"}', got '%s'", tc.Text)
		}
	}

	// Capture is synchronous, so the event is already published by now.
	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 2*time.Second)

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
	if !strings.HasPrefix(evt.GetSessionId(), "ses_") {
		t.Errorf("expected a minted ses_ session ID, got %q", evt.GetSessionId())
	}
	if evt.ProjectId != "proj_test" {
		t.Errorf("expected project ID 'proj_test', got %v", evt.ProjectId)
	}

	// Parameters record the raw request: tool name + arguments as sent.
	if evt.Parameters == nil {
		t.Fatal("expected non-nil parameters")
	}
	if evt.Parameters["name"] != "greet" {
		t.Errorf("expected param name 'greet', got %v", evt.Parameters["name"])
	}
	if args, ok := evt.Parameters["arguments"].(map[string]any); !ok || args["name"] != "World" {
		t.Errorf("expected raw arguments recorded, got %v", evt.Parameters["arguments"])
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

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 2*time.Second)
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
		schema := marshalToMap(t, tool.InputSchema)
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			continue
		}
		if _, exists := props["context"]; exists {
			t.Errorf("tool %s: context param should NOT be injected when disabled", tool.Name)
		}
	}
}

func TestMiddleware_WithIdentify(t *testing.T) {
	// The callback runs once per tool call, on that call's goroutine,
	// so the flag must be synchronized with the test goroutine.
	var identifyCalled atomic.Bool
	opts := DefaultOptions()
	opts.Identify = func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
		identifyCalled.Store(true)
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

	// v2: the identity stamps the tool-call event itself; no separate
	// agentcat:identify event is published.
	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(toolEvents) == 0 {
		t.Fatal("expected a tool call event")
	}

	if !identifyCalled.Load() {
		t.Error("expected Identify function to be called")
	}

	evt := toolEvents[0]
	if evt.IdentifyActorGivenId == nil || *evt.IdentifyActorGivenId != "user_123" {
		t.Errorf("expected identify actor ID 'user_123' on the tool-call event, got %v", evt.IdentifyActorGivenId)
	}
	if evt.IdentifyActorName == nil || *evt.IdentifyActorName != "Test User" {
		t.Errorf("expected identify actor name 'Test User' on the tool-call event, got %v", evt.IdentifyActorName)
	}
	if evt.IdentifyData["role"] != "admin" {
		t.Errorf("expected identify data stamped, got %v", evt.IdentifyData)
	}

	if identifies := filterEvents(mock.getEvents(), "agentcat:identify"); len(identifies) != 0 {
		t.Errorf("v2 publishes no agentcat:identify events, got %d", len(identifies))
	}
}

// TestMiddleware_IdentifyStampsEveryToolCall verifies the identify callback
// re-runs on every tool call and each call's event is stamped with the
// returned identity — even when the identity is unchanged (no dedup).
func TestMiddleware_IdentifyStampsEveryToolCall(t *testing.T) {
	identifyCount := 0
	var mu sync.Mutex
	opts := DefaultOptions()
	opts.Identify = func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
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
	waitForEventType(mock, "mcp:tools/call", 1, 2*time.Second)

	_, err = clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "Again"},
	})
	if err != nil {
		t.Fatalf("second CallTool error: %v", err)
	}

	// Wait for the second tool call event to be processed
	toolEvents := waitForEventType(mock, "mcp:tools/call", 2, 2*time.Second)

	mu.Lock()
	count := identifyCount
	mu.Unlock()

	// The callback re-runs on every tool call.
	if count != 2 {
		t.Errorf("expected Identify to be called on each tool call (2), got %d", count)
	}

	// Every tool-call event is stamped — no change-detection dedup.
	if len(toolEvents) != 2 {
		t.Fatalf("expected 2 tool-call events, got %d", len(toolEvents))
	}
	for i, evt := range toolEvents {
		if evt.IdentifyActorGivenId == nil || *evt.IdentifyActorGivenId != "user_123" {
			t.Errorf("event %d not stamped with identity: %v", i, evt.IdentifyActorGivenId)
		}
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

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 2*time.Second)
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

	// v2 records the raw request: context appears verbatim in the recorded
	// arguments AND as user intent (the handler still never sees it).
	if args, ok := evt.Parameters["arguments"].(map[string]any); !ok || args["context"] != "Greeting the user for a welcome message" {
		t.Errorf("raw arguments must include context verbatim, got %v", evt.Parameters["arguments"])
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

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 2*time.Second)
	if len(toolEvents) == 0 {
		t.Fatal("expected at least one tool call event")
	}

	evt := toolEvents[0]

	// Verify per-request metadata (legacy client: identity comes from the
	// initialize-params fallback; server info from the Track-time impl).
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

// TestStructuredContentAsMap pins the mirror's structuredContent conversion.
// Typed mcp.AddTool stores structuredContent as json.RawMessage, so the
// round-trip branch runs on every mirrored call: it must return an exact copy
// of the customer's JSON object — numbers included — and must refuse anything
// that is not a JSON object so the result is left untouched.
func TestStructuredContentAsMap(t *testing.T) {
	tests := []struct {
		name   string
		in     any
		wantOK bool
		want   string // canonical JSON of the returned map
	}{
		{name: "absent", in: nil, wantOK: false},
		{name: "nil raw message", in: json.RawMessage(nil), wantOK: false},
		{name: "map", in: map[string]any{"b": 2, "a": "x"}, wantOK: true, want: `{"a":"x","b":2}`},
		{name: "empty map", in: map[string]any{}, wantOK: true, want: `{}`},
		{name: "raw object", in: json.RawMessage(`{"text":"hi"}`), wantOK: true, want: `{"text":"hi"}`},
		{name: "raw array", in: json.RawMessage(`[1,2,3]`), wantOK: false},
		{name: "raw string", in: json.RawMessage(`"hello"`), wantOK: false},
		{name: "raw number", in: json.RawMessage(`42`), wantOK: false},
		{name: "raw null", in: json.RawMessage(`null`), wantOK: false},
		{name: "raw invalid json", in: json.RawMessage(`{nope`), wantOK: false},
		{name: "arbitrary value", in: struct {
			N int `json:"n"`
		}{N: 7}, wantOK: true, want: `{"n":7}`},
		{
			// The regression pin: float64 decoding would rewrite these as
			// 9.007199254740992e+15 and friends — a changed VALUE, and
			// schema-invalid against an "type":"integer" output schema.
			name:   "large integers survive verbatim",
			in:     json.RawMessage(`{"exact":9007199254740993,"id":1234567890123456789,"nested":{"huge":12345678901234567890},"ratio":1.5}`),
			wantOK: true,
			want:   `{"exact":9007199254740993,"id":1234567890123456789,"nested":{"huge":12345678901234567890},"ratio":1.5}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := structuredContentAsMap(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (got %v)", ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				if got != nil {
					t.Errorf("non-object input must return a nil map, got %v", got)
				}
				return
			}
			raw, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("re-marshal converted map: %v", err)
			}
			if string(raw) != tc.want {
				t.Errorf("converted map = %s, want %s", raw, tc.want)
			}
		})
	}

	// The map branch must hand back a COPY: the caller writes the mirror key
	// into the returned map, and the customer's own result must not change.
	orig := map[string]any{"a": 1}
	got, ok := structuredContentAsMap(orig)
	if !ok {
		t.Fatal("map input must convert")
	}
	got["_mcp_instructions"] = "mirror"
	if _, leaked := orig["_mcp_instructions"]; leaked {
		t.Error("conversion must copy: the customer's structuredContent was mutated")
	}
}

// TestStripArgumentsSafely_PanicFallsBackToRawArgs pins the strip seam's panic
// guard: the customer's call must dispatch with unmodified arguments rather
// than fail, matching every other seam on the request path.
func TestStripArgumentsSafely_PanicFallsBackToRawArgs(t *testing.T) {
	raw := map[string]any{"session_id": sid("x"), "payload": "keep"}

	got := stripArgumentsSafely(raw, func() map[string]any { panic("strip exploded") })
	if len(got) != len(raw) || got["session_id"] != sid("x") || got["payload"] != "keep" {
		t.Errorf("a panicking strip must fall back to the raw arguments, got %v", got)
	}

	// The happy path still strips.
	got = stripArgumentsSafely(raw, func() map[string]any {
		return agentcat.StripToolArguments("echo_args", raw, nil)
	})
	if _, has := got["session_id"]; has {
		t.Errorf("injected params must be stripped on the normal path, got %v", got)
	}
	if got["payload"] != "keep" {
		t.Errorf("real arguments must survive stripping, got %v", got)
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
