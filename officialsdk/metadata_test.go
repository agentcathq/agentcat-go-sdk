package officialsdk

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
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
	// SDK tags merge after customer tags on every tool-call event.
	if tags["agentcat_session_id_source"] == "" {
		t.Errorf("SDK session-source tag missing: %v", tags)
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
	// Customer tags are dropped; the SDK-owned tags still apply.
	if toolEvents[0].Tags == nil {
		t.Fatal("SDK tags must survive a customer EventTags panic")
	}
	tags := *toolEvents[0].Tags
	if _, ok := tags["env"]; ok {
		t.Errorf("customer tags must be dropped after panic, got %v", tags)
	}
	if tags["agentcat_session_id_source"] == "" {
		t.Errorf("SDK session-source tag missing after customer panic: %v", tags)
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

	settleForAbsentEvents()

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
		schema := marshalToMap(t, tool.InputSchema)
		if props, ok := schema["properties"].(map[string]any); ok {
			if _, ok := props["context"]; ok {
				found = true
			}
		}
	}
	if !found {
		t.Error("context parameter should still be injected when only tracing is disabled")
	}

	settleForAbsentEvents()
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
		schema := marshalToMap(t, tool.InputSchema)
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

// --- G11: Identify (v2: per-call, stamps the tool-call event only) ---

// TestIdentify_StampsEachToolCallEvent verifies the Identify callback re-runs
// on every tool call and its result is stamped on that call's event — with no
// separate agentcat:identify event and no cross-call identity merging.
func TestIdentify_StampsEachToolCallEvent(t *testing.T) {
	identities := []*agentcat.UserIdentity{
		{UserID: "u1", UserName: "Alice", UserData: map[string]any{"plan": "free"}},
		{UserID: "u1", UserName: "Alice", UserData: map[string]any{"plan": "free"}}, // identical to first
		{UserID: "u2", UserName: "Bob", UserData: map[string]any{"plan": "pro"}},
	}
	// The callback runs once per tool call, so the call counter
	// must be synchronized.
	var call atomic.Int64

	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.Identify = func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
		return identities[int(call.Add(1)-1)%len(identities)]
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
		// Capture is synchronous, so this call's event is already published;
		// the wait just pins that before moving on, keeping the identity
		// sequence ordered even if that ever changes.
		waitForEventType(mock, "mcp:tools/call", i+1, 3*time.Second)
	}

	events := mock.getEvents()
	if identifies := filterEvents(events, "agentcat:identify"); len(identifies) != 0 {
		t.Errorf("v2 publishes no agentcat:identify events, got %d", len(identifies))
	}

	toolEvents := filterEvents(events, "mcp:tools/call")
	if len(toolEvents) != 3 {
		t.Fatalf("expected 3 tool-call events, got %d", len(toolEvents))
	}

	// Each event carries exactly the identity returned for that call — used
	// verbatim, no merging across calls.
	for i, wantPlan := range []string{"free", "free", "pro"} {
		evt := toolEvents[i]
		if evt.IdentifyActorGivenId == nil {
			t.Fatalf("event %d missing identity", i)
		}
		if *evt.IdentifyActorGivenId != identities[i].UserID {
			t.Errorf("event %d actor = %q, want %q", i, *evt.IdentifyActorGivenId, identities[i].UserID)
		}
		if evt.IdentifyData["plan"] != wantPlan {
			t.Errorf("event %d plan = %v, want %s", i, evt.IdentifyData["plan"], wantPlan)
		}
	}
	// The third identity replaced the earlier one wholesale (stateless):
	// UserData is not merged across calls.
	if _, merged := toolEvents[2].IdentifyData["region"]; merged {
		t.Error("v2 must not merge identity data across calls")
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

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(toolEvents) == 0 {
		t.Fatal("tool call event should still be published when Identify panics")
	}
	if toolEvents[0].IdentifyActorGivenId != nil {
		t.Error("no identity should be stamped when Identify panics")
	}
}

// TestIdentify_RunsOnlyOnToolCalls verifies the v2 Identify contract: the
// callback fires once per tool call, inline on that call's own goroutine,
// and never for initialize, tools/list, or other methods — those publish
// nothing.
func TestIdentify_RunsOnlyOnToolCalls(t *testing.T) {
	var mu sync.Mutex
	seen := make(map[string]int)

	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.Identify = func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
		kind := "other"
		if ctr, ok := request.(*mcp.CallToolRequest); ok {
			kind = "tools/call " + ctr.Params.Name
		}
		mu.Lock()
		seen[kind]++
		mu.Unlock()
		return &agentcat.UserIdentity{UserID: "u-every", UserName: "Every Call"}
	}

	clientSession, _, mock := setupStreamableHTTP(t, opts) // performs the handshake
	ctx := context.Background()

	if _, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{}); err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "every call"},
	}); err != nil {
		t.Fatalf("CallTool error: %v", err)
	}

	toolEvents := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(toolEvents) != 1 {
		t.Fatalf("expected exactly 1 tool-call event, got %d", len(toolEvents))
	}
	if toolEvents[0].IdentifyActorGivenId == nil || *toolEvents[0].IdentifyActorGivenId != "u-every" {
		t.Errorf("tool-call event not stamped with identity: %v", toolEvents[0].IdentifyActorGivenId)
	}

	settleForAbsentEvents()

	mu.Lock()
	defer mu.Unlock()
	if seen["tools/call add_todo"] != 1 {
		t.Errorf("Identify must run once per tool call, saw %v", seen)
	}
	if seen["other"] != 0 {
		t.Errorf("Identify must not run for non-tool-call methods, saw %v", seen)
	}
}
