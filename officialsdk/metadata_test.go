package officialsdk

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk"
)

// waitForEventType polls until at least n events of the given type have been
// captured (or the timeout elapses) and returns the matching events.
func waitForEventType(mock *mockPublisher, eventType string, n int, timeout time.Duration) []*agentcat.Event {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		matched := filterEvents(mock.getEvents(), eventType)
		if len(matched) >= n {
			return matched
		}
		time.Sleep(10 * time.Millisecond)
	}
	return filterEvents(mock.getEvents(), eventType)
}

// --- G1: EventTags ---

func TestEventTags_AttachedToAutoCapturedEvents(t *testing.T) {
	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.EventTags = func(ctx context.Context, request mcp.Request) map[string]string {
		return map[string]string{
			"env":       "test",
			"trace_id":  "abc-123",
			"bad/key":   "dropped",
			"multiline": "a\nb",
		}
	}

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "tags test"},
	}); err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(toolEvents) == 0 {
		t.Fatal("expected a tools/call event")
	}

	evt := toolEvents[0]
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
	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.EventTags = func(ctx context.Context, request mcp.Request) map[string]string {
		panic("customer callback bug")
	}

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "panic test"},
	}); err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(toolEvents) == 0 {
		t.Fatal("event should still be published when EventTags panics")
	}
	if toolEvents[0].Tags != nil {
		t.Errorf("expected no tags after panic, got %v", *toolEvents[0].Tags)
	}
}

// --- G2: EventProperties ---

func TestEventProperties_AttachedToAutoCapturedEvents(t *testing.T) {
	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.EventProperties = func(ctx context.Context, request mcp.Request) map[string]any {
		return map[string]any{
			"device": "desktop",
			"nested": map[string]any{"a": 1},
		}
	}

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "properties test"},
	}); err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(toolEvents) == 0 {
		t.Fatal("expected a tools/call event")
	}

	evt := toolEvents[0]
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
	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.EventProperties = func(ctx context.Context, request mcp.Request) map[string]any {
		panic("customer callback bug")
	}

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "panic test"},
	}); err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(toolEvents) == 0 {
		t.Fatal("event should still be published when EventProperties panics")
	}
	if toolEvents[0].Properties != nil {
		t.Errorf("expected no properties after panic, got %v", toolEvents[0].Properties)
	}
}

// --- G10: DisableTracing ---

func TestDisableTracing_NoEventsPublished(t *testing.T) {
	opts := DefaultOptions()
	opts.DisableTracing = true

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "tracing disabled", "context": "why"},
	}); err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	// Give any (incorrect) async capture a moment to run.
	time.Sleep(200 * time.Millisecond)

	if events := mock.getEvents(); len(events) != 0 {
		t.Errorf("expected 0 events with DisableTracing, got %d", len(events))
	}
}

func TestDisableTracing_ContextInjectionStillWorks(t *testing.T) {
	opts := DefaultOptions()
	opts.DisableTracing = true
	opts.DisableReportMissing = true
	// DisableToolCallContext stays false: injection must still happen.

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	result, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}

	found := false
	for _, tool := range result.Tools {
		if tool.Name != "add_todo" {
			continue
		}
		schema := schemaToMap(tool.InputSchema)
		if props, ok := schema["properties"].(map[string]any); ok {
			if _, ok := props["context"]; ok {
				found = true
			}
		}
	}
	if !found {
		t.Error("context parameter should still be injected when only tracing is disabled")
	}

	time.Sleep(200 * time.Millisecond)
	if events := mock.getEvents(); len(events) != 0 {
		t.Errorf("expected 0 events with DisableTracing, got %d", len(events))
	}
}

// --- G7: CustomContextDescription ---

func TestCustomContextDescription_UsedInInjectedParam(t *testing.T) {
	const custom = "Explain the business objective this call helps achieve"
	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.CustomContextDescription = custom

	clientSession, _, _ := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	result, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}

	checked := false
	for _, tool := range result.Tools {
		if tool.Name != "add_todo" {
			continue
		}
		schema := schemaToMap(tool.InputSchema)
		props, _ := schema["properties"].(map[string]any)
		contextProp, ok := props["context"].(map[string]any)
		if !ok {
			t.Fatalf("context param not injected: %v", props)
		}
		if contextProp["description"] != custom {
			t.Errorf("context description = %q, want %q", contextProp["description"], custom)
		}
		checked = true
	}
	if !checked {
		t.Fatal("add_todo tool not found in list")
	}
}

// --- G9: Deterministic session IDs ---

func TestDeterministicSessionID_DerivedFromTransportSession(t *testing.T) {
	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "session derivation"},
	}); err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	rawSessionID := clientSession.ID()
	if rawSessionID == "" {
		t.Skip("transport did not expose a session ID")
	}

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(toolEvents) == 0 {
		t.Fatal("expected a tools/call event")
	}

	want := agentcat.DeriveSessionID(rawSessionID, "proj_test")
	if got := toolEvents[0].GetSessionId(); got != want {
		t.Errorf("event session ID = %q, want deterministic %q (raw %q)", got, want, rawSessionID)
	}
	if !strings.HasPrefix(toolEvents[0].GetSessionId(), "ses_") {
		t.Errorf("session ID missing ses_ prefix: %q", toolEvents[0].GetSessionId())
	}
}

// --- G11: Identify re-run + publish-every-time ---

func TestIdentify_PublishesEveryTime(t *testing.T) {
	identities := []*agentcat.UserIdentity{
		{UserID: "u1", UserName: "Alice", UserData: map[string]any{"plan": "free"}},
		{UserID: "u1", UserName: "Alice", UserData: map[string]any{"plan": "free"}}, // identical to first
		{UserID: "u1", UserName: "Alice", UserData: map[string]any{"plan": "pro"}},
	}
	var call int

	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.Identify = func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
		// Gate to tool calls so the identify-event count is driven only by
		// the three CallTool calls below.
		if _, ok := request.(*mcp.CallToolRequest); !ok {
			return nil
		}
		identity := identities[call%len(identities)]
		call++
		return identity
	}

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	for i, title := range []string{"first", "second", "third"} {
		if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name:      "add_todo",
			Arguments: map[string]any{"title": title},
		}); err != nil {
			t.Fatalf("CallTool %d error: %v", i, err)
		}
		// Captures run async: wait for this call's tools/call event before
		// the next call so the identify sequence stays ordered.
		waitForEventType(mock, "mcp:tools/call", i+1, 3*time.Second)
	}

	identifyEvents := filterEvents(mock.getEvents(), "agentcat:identify")

	if len(identifyEvents) != 3 {
		t.Fatalf("expected 3 identify events (one per identify call, no dedup), got %d", len(identifyEvents))
	}

	// The first two identify events carry the identical identity — proving
	// unchanged identities are still published (no change-detection dedup).
	if identifyEvents[0].IdentifyData["plan"] != "free" {
		t.Errorf("first identify event plan = %v, want free", identifyEvents[0].IdentifyData["plan"])
	}
	if identifyEvents[1].IdentifyData["plan"] != "free" {
		t.Errorf("second identify event plan = %v, want free", identifyEvents[1].IdentifyData["plan"])
	}
	if identifyEvents[2].IdentifyData["plan"] != "pro" {
		t.Errorf("third identify event plan = %v, want pro", identifyEvents[2].IdentifyData["plan"])
	}
}

func TestIdentify_MergesUserData(t *testing.T) {
	identities := []*agentcat.UserIdentity{
		{UserID: "u1", UserData: map[string]any{"region": "us", "plan": "free"}},
		{UserID: "u2", UserData: map[string]any{"plan": "pro"}},
	}
	var call int

	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.Identify = func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
		// Gate to tool calls so exactly two identify events are published.
		if _, ok := request.(*mcp.CallToolRequest); !ok {
			return nil
		}
		identity := identities[call%len(identities)]
		call++
		return identity
	}

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	for i, title := range []string{"first", "second"} {
		if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name:      "add_todo",
			Arguments: map[string]any{"title": title},
		}); err != nil {
			t.Fatalf("CallTool %d error: %v", i, err)
		}
		waitForEventType(mock, "mcp:tools/call", i+1, 3*time.Second)
	}

	identifyEvents := filterEvents(mock.getEvents(), "agentcat:identify")
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
	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.Identify = func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
		panic("customer identify bug")
	}

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "identify panic"},
	}); err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	if len(waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)) == 0 {
		t.Fatal("tool call event should still be published when Identify panics")
	}
	if len(filterEvents(mock.getEvents(), "agentcat:identify")) != 0 {
		t.Error("no identify event should be published when Identify panics")
	}
}

// TestIdentify_RunsOnEveryMethod verifies the Identify hook runs for every
// auto-captured MCP method (initialize, notifications, tools/list, tools/call)
// and that an identify event is published for each captured event when the
// callback returns an identity.
func TestIdentify_RunsOnEveryMethod(t *testing.T) {
	var mu sync.Mutex
	seenTypes := make(map[string]bool)

	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.Identify = func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
		// The go-sdk request types are aliases of generic instantiations
		// (e.g. CallToolRequest = ServerRequest[*CallToolParamsRaw]), so
		// classify via type assertions rather than %T strings. There is no
		// named alias for the server-side initialize request.
		var kind string
		switch request.(type) {
		case *mcp.CallToolRequest:
			kind = "tools/call"
		case *mcp.ListToolsRequest:
			kind = "tools/list"
		case *mcp.ServerRequest[*mcp.InitializeParams]:
			kind = "initialize"
		default:
			kind = fmt.Sprintf("%T", request)
		}
		mu.Lock()
		seenTypes[kind] = true
		mu.Unlock()
		return &agentcat.UserIdentity{UserID: "u-every", UserName: "Every Method"}
	}

	clientSession, _, mock := setupStreamableHTTP(t, opts) // performs initialize
	ctx := context.Background()

	if _, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{}); err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "every method"},
	}); err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	// initialize + notifications/initialized + tools/list + tools/call, each
	// paired with an identify event.
	events := mock.waitForEvents(8, 3*time.Second)

	// Loose set assertion: at least these request kinds must have been seen
	// (the initialized notification also fires, with its own request type).
	mu.Lock()
	for _, want := range []string{"initialize", "tools/list", "tools/call"} {
		if !seenTypes[want] {
			t.Errorf("Identify was not invoked for %s (saw %v)", want, seenTypes)
		}
	}
	mu.Unlock()

	var identifyCount, mcpCount int
	for _, evt := range events {
		if evt.EventType == nil {
			continue
		}
		switch {
		case *evt.EventType == "agentcat:identify":
			identifyCount++
		case strings.HasPrefix(*evt.EventType, "mcp:"):
			mcpCount++
		}
	}
	if mcpCount == 0 {
		t.Fatal("expected captured mcp:* events")
	}
	if identifyCount != mcpCount {
		t.Errorf("expected one identify event per captured mcp event: identify=%d, mcp=%d", identifyCount, mcpCount)
	}
}
