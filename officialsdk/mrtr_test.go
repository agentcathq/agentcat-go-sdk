//go:build !gosdk_legacy

package officialsdk

// Multi-round-trip arrived in go-sdk v1.7.0 with the 2026-07-28 protocol.
// These tests build mcp.InputRequestMap / mcp.InputResponseMap literals, which
// no feature-detection shim can stand in for, so the gosdk_legacy tag excludes
// the file when building against an older go-sdk. See compat.go.

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// ── fixtures ─────────────────────────────────────────────────────────────────

// wireRecorder records the tools/call results that leave the server, exactly as
// the transport marshals them. Installed as the OUTERMOST receiving middleware
// (added after the tracking middleware), so it sees the decorated wire copy —
// not the customer's original result.
type wireRecorder struct {
	mu      sync.Mutex
	results []*mcp.CallToolResult
}

func (w *wireRecorder) middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if method == "tools/call" {
				if ctr, ok := res.(*mcp.CallToolResult); ok {
					w.mu.Lock()
					w.results = append(w.results, ctr)
					w.mu.Unlock()
				}
			}
			return res, err
		}
	}
}

func (w *wireRecorder) rounds() []*mcp.CallToolResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*mcp.CallToolResult(nil), w.results...)
}

// elicitSchema is the (valid) form schema the confirm_action fixture requests.
var elicitSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"ok": map[string]any{"type": "boolean"},
	},
}

// newMRTRServer builds a tracked server whose confirm_action tool asks the
// client for input on its first round and completes on the continuation — the
// SEP-2322 multi round-trip shape.
func newMRTRServer(t *testing.T) (*mcp.Server, *mockPublisher, *wireRecorder) {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "mrtr-test-server", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "confirm_action",
		Description: "Asks the client to confirm, then acts",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"action": map[string]any{"type": "string"}},
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if len(req.Params.InputResponses) == 0 {
			// Round 1: content and inputRequests are mutually exclusive.
			return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{
				"confirm": &mcp.ElicitParams{
					Mode:            "form",
					Message:         "Confirm this action?",
					RequestedSchema: elicitSchema,
				},
			}}, nil
		}
		// Round 2: the completing round.
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "confirmed"}}}, nil
	})

	mock := &mockPublisher{}
	opts := DefaultOptions()
	opts.DisableReportMissing = true
	coreOpts := &agentcat.Options{DisableReportMissing: true}
	agentcat.RegisterServer(server, &agentcat.AgentCatInstance{ProjectID: "proj_mrtr", Options: coreOpts})
	t.Cleanup(func() { agentcat.UnregisterServer(server) })

	middleware := newTrackingMiddleware(server, "proj_mrtr", opts, mock.publish,
		&mcp.Implementation{Name: "mrtr-test-server", Version: "1.0.0"})
	server.AddReceivingMiddleware(middleware)

	// Outermost: whatever this sees is what goes on the wire.
	recorder := &wireRecorder{}
	server.AddReceivingMiddleware(recorder.middleware())

	return server, mock, recorder
}

// connectElicitingClient connects a real client with an elicitation handler over
// in-memory transports. legacy pins the pre-2026 initialize handshake, which is
// what routes the round trip through the go-sdk's server-side compatibility
// shim instead of back out to the client.
func connectElicitingClient(t *testing.T, server *mcp.Server, legacy bool, elicits *atomic.Int32) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "mrtr-test-client", Version: "1.0.0"}, &mcp.ClientOptions{
		ElicitationHandler: func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			elicits.Add(1)
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"ok": true}}, nil
		},
	})
	if legacy {
		pinLegacyInitializeHandshake(client)
	}
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		cs.Close()
		serverSession.Wait()
	})
	return cs
}

// mrtrTag returns the value of the SDK's MRTR tag on an event ("" when absent).
func mrtrTag(evt *agentcat.Event) string {
	if evt.Tags == nil {
		return ""
	}
	return (*evt.Tags)[agentcat.TagMRTR]
}

// eventByMRTR returns the one event carrying the given MRTR tag, failing if
// there is not exactly one.
//
// Capture is synchronous, but two calls may still overlap, so the order of
// mock.events is not guaranteed even when the requests are strictly ordered on
// the wire. Rounds are therefore selected by what they ARE, never by position.
func eventByMRTR(t *testing.T, events []*agentcat.Event, tag string) *agentcat.Event {
	t.Helper()
	var found *agentcat.Event
	n := 0
	for _, evt := range events {
		if mrtrTag(evt) == tag {
			found = evt
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 event tagged %s=%q, got %d", agentcat.TagMRTR, tag, n)
	}
	return found
}

// ── 2026 client: the round trip goes back out to the client ──────────────────

// TestMRTRModernClientPublishesTaggedPair drives a real multi round-trip against
// a 2026-protocol client. The server hands the input request back to the client,
// which fulfills it and retries: two tools/call requests reach the SDK, so it
// publishes two events for the one logical call — tagged input_required and
// continuation, both attributed to the supplied session.
func TestMRTRModernClientPublishesTaggedPair(t *testing.T) {
	server, mock, recorder := newMRTRServer(t)
	var elicits atomic.Int32
	cs := connectElicitingClient(t, server, false, &elicits)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "confirm_action",
		Arguments: map[string]any{"action": "delete", "session_id": sid("mrtr_e2e")},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.NeedsInput() {
		t.Fatal("the client must have completed the round trip")
	}
	if got := len(res.Content); got == 0 {
		t.Fatal("completing round returned no content")
	}
	if elicits.Load() != 1 {
		t.Errorf("elicitation handler ran %d times, want 1", elicits.Load())
	}

	events := waitForEventType(mock, "mcp:tools/call", 2, 3*time.Second)
	settleForAbsentEvents()
	events = filterEvents(mock.getEvents(), "mcp:tools/call")
	if len(events) != 2 {
		t.Fatalf("expected exactly 2 events for the logical call, got %d", len(events))
	}

	// Exactly one of each tag, selected by tag rather than by arrival order.
	intermediate := eventByMRTR(t, events, agentcat.MRTRInputRequired)
	completing := eventByMRTR(t, events, agentcat.MRTRContinuation)
	for name, evt := range map[string]*agentcat.Event{
		"input_required": intermediate,
		"continuation":   completing,
	} {
		if evt.GetSessionId() != sid("mrtr_e2e") {
			t.Errorf("%s round attributed to %q, want ses_mrtr_e2e", name, evt.GetSessionId())
		}
	}

	// Two real request/response rounds crossed the transport.
	rounds := recorder.rounds()
	if len(rounds) != 2 {
		t.Fatalf("expected 2 wire results, got %d", len(rounds))
	}
	if !rounds[0].NeedsInput() {
		t.Error("first wire result must be the input-required round")
	}
	if rounds[1].NeedsInput() {
		t.Error("second wire result must be the completing round")
	}
}

// TestMRTRIntermediateRoundCarriesNoMintBack pins the wire-level rule: the
// intermediate (input-required) round is never decorated, even when the call
// minted a session — the completing round carries the mint-back instead.
//
// Omitting session_id is what makes this non-vacuous: with a minted session the
// decoration WOULD be applied on any ordinary result.
func TestMRTRIntermediateRoundCarriesNoMintBack(t *testing.T) {
	server, mock, recorder := newMRTRServer(t)
	var elicits atomic.Int32
	cs := connectElicitingClient(t, server, false, &elicits)

	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "confirm_action",
		Arguments: map[string]any{"action": "delete"},
	}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	events := waitForEventType(mock, "mcp:tools/call", 2, 3*time.Second)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	rounds := recorder.rounds()
	if len(rounds) != 2 {
		t.Fatalf("expected 2 wire results, got %d", len(rounds))
	}

	// Round 1: the handler returned an empty result and nothing was appended to
	// it, so the content must still be empty — not merely free of a mint-back.
	if got := len(rounds[0].Content); got != 0 {
		t.Errorf("the input-required round must stay undecorated, got %d content block(s): %v",
			got, rounds[0].Content)
	}
	if rounds[0].StructuredContent != nil {
		t.Errorf("the input-required round must stay undecorated, got structured content %v",
			rounds[0].StructuredContent)
	}

	// Round 2 minted its own session and DID carry the mint-back for it: the same
	// decoration the code deliberately skipped one round earlier. Selected by
	// its MRTR tag: rounds may overlap, so arrival order proves nothing.
	minted := eventByMRTR(t, events, agentcat.MRTRContinuation).GetSessionId()
	if !strings.HasPrefix(minted, "ses_") {
		t.Fatalf("completing round has no minted session: %q", minted)
	}
	want := mintBackFor(minted)
	found := false
	for _, c := range rounds[1].Content {
		if tc, ok := c.(*mcp.TextContent); ok && tc.Text == want {
			found = true
		}
	}
	if !found {
		t.Errorf("completing round is missing the mint-back for %s", minted)
	}
}

// ── legacy client: the go-sdk completes the round trip inside the server ─────

// TestMRTRLegacyClientPublishesOneUntaggedEvent pins the pre-2026 path: the
// go-sdk's server-side compatibility shim elicits from the client and reinvokes
// the handler internally, so only ONE tools/call ever reaches AgentCat's
// middleware — one event, and no MRTR tag on it.
func TestMRTRLegacyClientPublishesOneUntaggedEvent(t *testing.T) {
	server, mock, recorder := newMRTRServer(t)
	var elicits atomic.Int32
	cs := connectElicitingClient(t, server, true, &elicits)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "confirm_action",
		Arguments: map[string]any{"action": "delete", "session_id": sid("mrtr_legacy")},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.NeedsInput() {
		t.Fatal("a legacy client must never see an input-required result")
	}
	if elicits.Load() != 1 {
		t.Errorf("elicitation handler ran %d times, want 1", elicits.Load())
	}

	events := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	settleForAbsentEvents()
	events = filterEvents(mock.getEvents(), "mcp:tools/call")
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event (the shim completes the round internally), got %d", len(events))
	}
	if got := mrtrTag(events[0]); got != "" {
		t.Errorf("mrtr tag = %q, want none — the SDK never saw a multi round-trip", got)
	}
	if events[0].GetSessionId() != sid("mrtr_legacy") {
		t.Errorf("event attributed to %q, want ses_mrtr_legacy", events[0].GetSessionId())
	}

	// One request crossed the middleware, and its result was the completing one.
	rounds := recorder.rounds()
	if len(rounds) != 1 {
		t.Fatalf("expected 1 wire result, got %d", len(rounds))
	}
	if rounds[0].NeedsInput() {
		t.Error("the shim must hand the client a completed result")
	}
}
