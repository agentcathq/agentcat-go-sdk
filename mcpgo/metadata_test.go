package mcpgo

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	agentcat "go.agentcat.com/sdk/v2"
)

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

// TestEventTags_PanicIsSwallowed verifies the customer's tags are dropped when
// their callback panics while the event — and the SDK's own tags — survive.
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
	assertOnlySDKTags(t, evt)
}

// TestEventTags_NilResultOmitsTags verifies a nil customer result contributes
// no tags; the SDK's own tags are still stamped.
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
	assertOnlySDKTags(t, evt)
}

// assertOnlySDKTags fails unless the event carries the SDK's own tags and
// nothing else.
func assertOnlySDKTags(t *testing.T, evt *agentcat.Event) {
	t.Helper()
	if evt.Tags == nil {
		t.Fatal("SDK tags are always stamped on tool-call events")
	}
	tags := *evt.Tags
	if _, ok := tags["agentcat_session_id_source"]; !ok {
		t.Errorf("SDK session source tag missing: %v", tags)
	}
	for key := range tags {
		if !strings.HasPrefix(key, "agentcat_") {
			t.Errorf("unexpected customer tag %q survived: %v", key, tags)
		}
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

	settleForAbsentEvents()

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

// --- G9: session attribution is stateless ---

// TestSessionIDIgnoresTransportSession pins that the transport session ID plays
// no part in session attribution: two calls on one HTTP session get two distinct
// minted sessions, and neither is derived from the session ID.
func TestSessionIDIgnoresTransportSession(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	}

	mcpClient, mock := setupSpyHTTP(t, opts)
	callAddTodo(t, mcpClient, "first")
	callAddTodo(t, mcpClient, "second")

	events := filterEvents(mock.waitForEvents(2, 2*time.Second), "mcp:tools/call")
	if len(events) != 2 {
		t.Fatalf("expected 2 tool-call events, got %d", len(events))
	}
	if events[0].GetSessionId() == events[1].GetSessionId() {
		t.Error("each minted call gets its own session; nothing is keyed off the transport session")
	}

	rawSessionID := mcpClient.GetSessionId()
	if rawSessionID == "" {
		return
	}
	derived := agentcat.DeriveSessionID(rawSessionID, "test_project")
	for i, evt := range events {
		if evt.GetSessionId() == derived {
			t.Errorf("event %d session is derived from the transport session ID (%q)", i, rawSessionID)
		}
		if !strings.HasPrefix(evt.GetSessionId(), "ses_") {
			t.Errorf("session ID missing ses_ prefix: %q", evt.GetSessionId())
		}
	}
}

// --- G11: Identify re-runs per tool call and stamps that event ---

// TestIdentify_StampsEachToolCallEvent verifies Identify runs on every tool
// call with no dedup, and that each result stamps its own event (v2 publishes
// no separate identify event).
func TestIdentify_StampsEachToolCallEvent(t *testing.T) {
	identities := []*agentcat.UserIdentity{
		{UserID: "u1", UserName: "Alice", UserData: map[string]any{"plan": "free"}},
		{UserID: "u1", UserName: "Alice", UserData: map[string]any{"plan": "free"}}, // identical to first
		{UserID: "u1", UserName: "Alice", UserData: map[string]any{"plan": "pro"}},
	}
	// The callback runs on server request-handling goroutines, so the call
	// counter must be synchronized with the test goroutine.
	var call atomic.Int64

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
			return identities[int(call.Add(1)-1)%len(identities)]
		},
	}

	mcpClient, mock := setupSpyHTTP(t, opts)

	callAddTodo(t, mcpClient, "first")
	callAddTodo(t, mcpClient, "second")
	callAddTodo(t, mcpClient, "third")

	events := filterEvents(mock.waitForEvents(3, 3*time.Second), "mcp:tools/call")
	if len(events) != 3 {
		t.Fatalf("expected 3 tool-call events, got %d", len(events))
	}

	// Every event is stamped — unchanged identities included (no dedup).
	for i, evt := range events {
		if evt.IdentifyActorGivenId == nil || *evt.IdentifyActorGivenId != "u1" {
			t.Errorf("event %d actor = %v, want u1", i, evt.IdentifyActorGivenId)
		}
	}
	for i, want := range []string{"free", "free", "pro"} {
		if got := events[i].IdentifyData["plan"]; got != want {
			t.Errorf("event %d plan = %v, want %s", i, got, want)
		}
	}
	if len(filterEvents(mock.getEvents(), "agentcat:identify")) != 0 {
		t.Error("v2 publishes no separate identify event")
	}
}

// TestIdentify_DoesNotMergeAcrossCalls is the inverse of the v1 session-merge
// behavior: identity is per call, verbatim, with nothing carried over.
func TestIdentify_DoesNotMergeAcrossCalls(t *testing.T) {
	identities := []*agentcat.UserIdentity{
		{UserID: "u1", UserData: map[string]any{"region": "us", "plan": "free"}},
		{UserID: "u2", UserData: map[string]any{"plan": "pro"}},
	}
	var call atomic.Int64

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
			return identities[int(call.Add(1)-1)%len(identities)]
		},
	}

	mcpClient, mock := setupSpyHTTP(t, opts)

	callAddTodo(t, mcpClient, "first")
	callAddTodo(t, mcpClient, "second")

	events := filterEvents(mock.waitForEvents(2, 3*time.Second), "mcp:tools/call")
	if len(events) != 2 {
		t.Fatalf("expected 2 tool-call events, got %d", len(events))
	}

	second := events[1]
	if second.IdentifyActorGivenId == nil || *second.IdentifyActorGivenId != "u2" {
		t.Errorf("second event actor = %v, want u2", second.IdentifyActorGivenId)
	}
	if second.IdentifyData["plan"] != "pro" {
		t.Errorf("plan = %v, want pro", second.IdentifyData["plan"])
	}
	if _, has := second.IdentifyData["region"]; has {
		t.Errorf("nothing carries over between calls, got %v", second.IdentifyData)
	}
}

func TestIdentify_PanicIsSwallowed(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
			panic("customer identify bug")
		},
	}

	mcpClient, mock := setupSpyHTTP(t, opts)
	callAddTodo(t, mcpClient, "identify panic")

	events := mock.waitForEvents(1, 2*time.Second)
	evt := findEventByType(events, "mcp:tools/call")
	if evt == nil {
		t.Fatal("tool call event should still be published when Identify panics")
	}
	if evt.IdentifyActorGivenId != nil {
		t.Errorf("no actor should be stamped when Identify panics, got %q", *evt.IdentifyActorGivenId)
	}
}

// TestIdentify_RunsOnlyOnToolCalls verifies the v2 contract: Identify runs once
// per tool call and never for other MCP methods (which publish no events).
func TestIdentify_RunsOnlyOnToolCalls(t *testing.T) {
	var mu sync.Mutex
	seenTypes := make(map[string]bool)

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
			mu.Lock()
			seenTypes[fmt.Sprintf("%T", request)] = true
			mu.Unlock()
			return &agentcat.UserIdentity{UserID: "u-every", UserName: "Every Call"}
		},
	}

	mcpClient, mock := setupSpyHTTP(t, opts) // performs initialize

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{}); err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	callAddTodo(t, mcpClient, "only tool calls")

	events := mock.waitForEvents(1, 3*time.Second)

	mu.Lock()
	if !seenTypes["*mcp.CallToolRequest"] {
		t.Errorf("Identify must run for tool calls (saw %v)", seenTypes)
	}
	for seen := range seenTypes {
		if seen != "*mcp.CallToolRequest" {
			t.Errorf("Identify must not run for %s", seen)
		}
	}
	mu.Unlock()

	if got := len(filterEvents(events, "mcp:tools/call")); got != 1 {
		t.Errorf("expected exactly 1 tool-call event, got %d of %v", got, eventTypes(events))
	}
	if len(events) != 1 {
		t.Errorf("expected only the tool-call event, got %v", eventTypes(events))
	}
}
