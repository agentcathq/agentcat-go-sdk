package mcpgo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
)

// setupSpyHTTP wires up a full HTTP transport (like setupStreamableHTTP) but
// with the tracing hooks publishing into a mockPublisher, so tests can assert
// on the exact events that would be sent to MCPCat.
func setupSpyHTTP(t *testing.T, opts *Options) (*client.Client, *mockPublisher) {
	t.Helper()

	mcpServer, _ := CreateFullServer()

	hooks := &server.Hooks{}
	server.WithHooks(hooks)(mcpServer)

	mock := &mockPublisher{}
	sessionMap := addTracingToHooks(hooks, opts, mock.publish)
	t.Cleanup(sessionMap.Stop)

	instance := &agentcat.AgentCatInstance{
		ProjectID: "test_project",
		Options: &agentcat.Options{
			DisableReportMissing:     opts.DisableReportMissing,
			DisableToolCallContext:   opts.DisableToolCallContext,
			DisableTracing:           opts.DisableTracing,
			CustomContextDescription: opts.CustomContextDescription,
		},
		ServerRef: mcpServer,
		SessionID: agentcat.NewSessionID(),
	}
	agentcat.RegisterServer(mcpServer, instance)

	httpServer := server.NewTestStreamableHTTPServer(mcpServer)

	mcpClient, err := client.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		httpServer.Close()
		unregisterServer(mcpServer)
		t.Fatalf("setupSpyHTTP: NewStreamableHttpClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	if err := mcpClient.Start(ctx); err != nil {
		cancel()
		mcpClient.Close()
		httpServer.Close()
		unregisterServer(mcpServer)
		t.Fatalf("setupSpyHTTP: client.Start failed: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "spy-http-client",
		Version: "1.0.0",
	}

	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		cancel()
		mcpClient.Close()
		httpServer.Close()
		unregisterServer(mcpServer)
		t.Fatalf("setupSpyHTTP: Initialize failed: %v", err)
	}

	t.Cleanup(func() {
		mcpClient.Close()
		httpServer.Close()
		cancel()
		unregisterServer(mcpServer)
	})

	return mcpClient, mock
}

func callAddTodo(t *testing.T, mcpClient *client.Client, title string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "add_todo"
	req.Params.Arguments = map[string]any{"title": title}

	if _, err := mcpClient.CallTool(ctx, req); err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
}

func findEventByType(events []*agentcat.Event, eventType string) *agentcat.Event {
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == eventType {
			return evt
		}
	}
	return nil
}

// --- G1: EventTags ---

func TestEventTags_AttachedToAutoCapturedEvents(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		EventTags: func(ctx context.Context, request any) map[string]string {
			return map[string]string{
				"env":       "test",
				"trace_id":  "abc-123",
				"bad/key":   "dropped",
				"multiline": "a\nb",
			}
		},
	}

	mcpClient, mock := setupSpyHTTP(t, opts)
	callAddTodo(t, mcpClient, "tags test")

	events := mock.waitForEvents(1, 2*time.Second)
	evt := findEventByType(events, "mcp:tools/call")
	if evt == nil {
		t.Fatalf("no tools/call event captured; got %d events", len(events))
	}

	if evt.Tags == nil {
		t.Fatal("expected tags on tool call event")
	}
	tags := *evt.Tags
	if tags["env"] != "test" || tags["trace_id"] != "abc-123" {
		t.Errorf("valid tags missing: %v", tags)
	}
	if _, ok := tags["bad/key"]; ok {
		t.Error("invalid key should have been dropped")
	}
	if _, ok := tags["multiline"]; ok {
		t.Error("newline value should have been dropped")
	}
}

func TestEventTags_PanicIsSwallowed(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		EventTags: func(ctx context.Context, request any) map[string]string {
			panic("customer callback bug")
		},
	}

	mcpClient, mock := setupSpyHTTP(t, opts)
	callAddTodo(t, mcpClient, "panic test")

	events := mock.waitForEvents(1, 2*time.Second)
	evt := findEventByType(events, "mcp:tools/call")
	if evt == nil {
		t.Fatal("event should still be published when EventTags panics")
	}
	if evt.Tags != nil {
		t.Errorf("expected no tags after panic, got %v", *evt.Tags)
	}
}

func TestEventTags_NilResultOmitsTags(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		EventTags: func(ctx context.Context, request any) map[string]string {
			return nil
		},
	}

	mcpClient, mock := setupSpyHTTP(t, opts)
	callAddTodo(t, mcpClient, "nil tags")

	events := mock.waitForEvents(1, 2*time.Second)
	evt := findEventByType(events, "mcp:tools/call")
	if evt == nil {
		t.Fatal("no tools/call event captured")
	}
	if evt.Tags != nil {
		t.Errorf("expected nil tags, got %v", *evt.Tags)
	}
}

// --- G2: EventProperties ---

func TestEventProperties_AttachedToAutoCapturedEvents(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		EventProperties: func(ctx context.Context, request any) map[string]any {
			return map[string]any{
				"device":        "desktop",
				"feature_flags": []any{"dark_mode", "beta_ui"},
				"nested":        map[string]any{"a": 1},
			}
		},
	}

	mcpClient, mock := setupSpyHTTP(t, opts)
	callAddTodo(t, mcpClient, "properties test")

	events := mock.waitForEvents(1, 2*time.Second)
	evt := findEventByType(events, "mcp:tools/call")
	if evt == nil {
		t.Fatal("no tools/call event captured")
	}

	if evt.Properties == nil {
		t.Fatal("expected properties on tool call event")
	}
	if evt.Properties["device"] != "desktop" {
		t.Errorf("properties missing device: %v", evt.Properties)
	}
	if nested, ok := evt.Properties["nested"].(map[string]any); !ok || nested["a"] != 1 {
		t.Errorf("nested properties not preserved: %v", evt.Properties["nested"])
	}
}

func TestEventProperties_PanicIsSwallowed(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		EventProperties: func(ctx context.Context, request any) map[string]any {
			panic("customer callback bug")
		},
	}

	mcpClient, mock := setupSpyHTTP(t, opts)
	callAddTodo(t, mcpClient, "panic test")

	events := mock.waitForEvents(1, 2*time.Second)
	evt := findEventByType(events, "mcp:tools/call")
	if evt == nil {
		t.Fatal("event should still be published when EventProperties panics")
	}
	if evt.Properties != nil {
		t.Errorf("expected no properties after panic, got %v", evt.Properties)
	}
}

// --- G10: DisableTracing ---

func TestDisableTracing_NoEventsPublished(t *testing.T) {
	opts := &Options{
		DisableTracing: true,
	}

	mcpClient, mock := setupSpyHTTP(t, opts)
	callAddTodo(t, mcpClient, "tracing disabled")

	// Give any (incorrect) capture a moment to run.
	time.Sleep(100 * time.Millisecond)

	if events := mock.getEvents(); len(events) != 0 {
		t.Errorf("expected 0 events with DisableTracing, got %d", len(events))
	}
}

func TestDisableTracing_ContextInjectionStillWorks(t *testing.T) {
	opts := &Options{
		DisableTracing: true,
		// DisableToolCallContext stays false: injection must still happen.
	}

	mcpClient, mock := setupSpyHTTP(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	found := false
	for _, tool := range result.Tools {
		if tool.Name == "add_todo" {
			if _, ok := tool.InputSchema.Properties["context"]; ok {
				found = true
			}
		}
	}
	if !found {
		t.Error("context parameter should still be injected when only tracing is disabled")
	}

	if events := mock.getEvents(); len(events) != 0 {
		t.Errorf("expected 0 events with DisableTracing, got %d", len(events))
	}
}

// --- G7: CustomContextDescription ---

func TestCustomContextDescription_UsedInInjectedParam(t *testing.T) {
	const custom = "Explain the business objective this call helps achieve"
	opts := &Options{
		DisableReportMissing:     true,
		CustomContextDescription: custom,
	}

	mcpClient, _ := setupSpyHTTP(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	checked := false
	for _, tool := range result.Tools {
		if tool.Name != "add_todo" {
			continue
		}
		prop, ok := tool.InputSchema.Properties["context"].(map[string]any)
		if !ok {
			t.Fatalf("context param not injected: %v", tool.InputSchema.Properties)
		}
		if prop["description"] != custom {
			t.Errorf("context description = %q, want %q", prop["description"], custom)
		}
		checked = true
	}
	if !checked {
		t.Fatal("add_todo tool not found in list")
	}
}

// --- G9: Deterministic session IDs ---

func TestDeterministicSessionID_DerivedFromTransportSession(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	}

	mcpClient, mock := setupSpyHTTP(t, opts)
	callAddTodo(t, mcpClient, "session derivation")

	rawSessionID := mcpClient.GetSessionId()
	if rawSessionID == "" {
		t.Skip("transport did not expose a session ID")
	}

	events := mock.waitForEvents(1, 2*time.Second)
	evt := findEventByType(events, "mcp:tools/call")
	if evt == nil {
		t.Fatal("no tools/call event captured")
	}

	want := agentcat.DeriveSessionID(rawSessionID, "test_project")
	if got := evt.GetSessionId(); got != want {
		t.Errorf("event session ID = %q, want deterministic %q (raw %q)", got, want, rawSessionID)
	}
	if !strings.HasPrefix(evt.GetSessionId(), "ses_") {
		t.Errorf("session ID missing ses_ prefix: %q", evt.GetSessionId())
	}
}

// --- G11: Identify re-run + change detection ---

func TestIdentify_PublishesOnlyOnChange(t *testing.T) {
	identities := []*agentcat.UserIdentity{
		{UserID: "u1", UserName: "Alice", UserData: map[string]any{"plan": "free"}},
		{UserID: "u1", UserName: "Alice", UserData: map[string]any{"plan": "free"}}, // unchanged
		{UserID: "u1", UserName: "Alice", UserData: map[string]any{"plan": "pro"}},  // changed
	}
	var call int

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request *mcp.CallToolRequest) *agentcat.UserIdentity {
			identity := identities[call%len(identities)]
			call++
			return identity
		},
	}

	mcpClient, mock := setupSpyHTTP(t, opts)

	callAddTodo(t, mcpClient, "first")
	callAddTodo(t, mcpClient, "second")
	callAddTodo(t, mcpClient, "third")

	events := mock.waitForEvents(5, 3*time.Second)

	var identifyEvents []*agentcat.Event
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == "agentcat:identify" {
			identifyEvents = append(identifyEvents, evt)
		}
	}

	if len(identifyEvents) != 2 {
		t.Fatalf("expected 2 identify events (initial + change), got %d", len(identifyEvents))
	}

	// The second identify event must carry the merged (changed) identity.
	second := identifyEvents[1]
	if second.IdentifyData["plan"] != "pro" {
		t.Errorf("changed identify event plan = %v, want pro", second.IdentifyData["plan"])
	}
}

func TestIdentify_MergesUserData(t *testing.T) {
	identities := []*agentcat.UserIdentity{
		{UserID: "u1", UserData: map[string]any{"region": "us", "plan": "free"}},
		{UserID: "u2", UserData: map[string]any{"plan": "pro"}},
	}
	var call int

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request *mcp.CallToolRequest) *agentcat.UserIdentity {
			identity := identities[call%len(identities)]
			call++
			return identity
		},
	}

	mcpClient, mock := setupSpyHTTP(t, opts)

	callAddTodo(t, mcpClient, "first")
	callAddTodo(t, mcpClient, "second")

	events := mock.waitForEvents(4, 3*time.Second)

	var identifyEvents []*agentcat.Event
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == "agentcat:identify" {
			identifyEvents = append(identifyEvents, evt)
		}
	}
	if len(identifyEvents) != 2 {
		t.Fatalf("expected 2 identify events, got %d", len(identifyEvents))
	}

	merged := identifyEvents[1]
	if merged.IdentifyActorGivenId == nil || *merged.IdentifyActorGivenId != "u2" {
		t.Errorf("UserID should be overwritten to u2, got %v", merged.IdentifyActorGivenId)
	}
	if merged.IdentifyData["plan"] != "pro" {
		t.Errorf("plan should be overwritten to pro, got %v", merged.IdentifyData["plan"])
	}
	if merged.IdentifyData["region"] != "us" {
		t.Errorf("region should be preserved from previous identity, got %v", merged.IdentifyData["region"])
	}
}

func TestIdentify_PanicIsSwallowed(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request *mcp.CallToolRequest) *agentcat.UserIdentity {
			panic("customer identify bug")
		},
	}

	mcpClient, mock := setupSpyHTTP(t, opts)
	callAddTodo(t, mcpClient, "identify panic")

	events := mock.waitForEvents(1, 2*time.Second)
	if evt := findEventByType(events, "mcp:tools/call"); evt == nil {
		t.Fatal("tool call event should still be published when Identify panics")
	}
	if evt := findEventByType(events, "agentcat:identify"); evt != nil {
		t.Error("no identify event should be published when Identify panics")
	}
}
