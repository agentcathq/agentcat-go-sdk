package mcpgo

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/v2"
)

// newOwnSessionServer builds a server whose single tool declares its OWN
// session_id — the collision case. AgentCat must neither inject into it nor
// read it, and the customer's value must reach the handler untouched.
func newOwnSessionServer(received *[]string) *server.MCPServer {
	mcpServer := server.NewMCPServer("own-session-server", "1.0.0", server.WithToolCapabilities(true))
	mcpServer.AddTool(
		mcp.NewTool("echo",
			mcp.WithDescription("declares its own session_id"),
			mcp.WithString("text", mcp.Description("what to echo")),
			mcp.WithString("session_id", mcp.Description("the customer's own correlation id")),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			v, _ := request.GetArguments()["session_id"].(string)
			*received = append(*received, v)
			return mcp.NewToolResultText("ok"), nil
		},
	)
	return mcpServer
}

func sessionSourceTag(t *testing.T, evt *agentcat.Event) string {
	t.Helper()
	if evt.Tags == nil {
		t.Fatal("event carries no tags")
	}
	return (*evt.Tags)["agentcat_session_id_source"]
}

// callOnce drives one tools/call and returns the single event it published.
func callOnce(t *testing.T, h *spyHarness, tool string, args map[string]any) (*mcp.CallToolResult, *agentcat.Event) {
	t.Helper()
	before := len(filterEvents(h.Mock.getEvents(), "mcp:tools/call"))
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	res, err := h.Client.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool %s: %v", tool, err)
	}
	events := filterEvents(h.Mock.getEvents(), "mcp:tools/call")
	if len(events) != before+1 {
		t.Fatalf("expected exactly 1 event, got %d", len(events)-before)
	}
	return res, events[before]
}

// TestInvalidSessionPublishesSessionless is the core of the trust model: a
// session_id this server never issued is not adopted into Event.SessionId,
// which is exempt from both redaction hooks. The raw value must still reach
// the recorded arguments, where redaction CAN reach it.
func TestInvalidSessionPublishesSessionless(t *testing.T) {
	h := newSpyHarness(t, nil)

	const forged = "sk_live_not_a_session"
	_, evt := callOnce(t, h, "add_todo", map[string]any{
		"title": "hi", "session_id": forged, "context": "trying a made-up handle",
	})

	if evt.SessionId.Get() != nil {
		t.Errorf("sessionId = %q; a value this server never issued must not be adopted", evt.GetSessionId())
	}
	if got := sessionSourceTag(t, evt); got != "invalid" {
		t.Errorf("session source tag = %q, want invalid", got)
	}
	args, _ := evt.Parameters["arguments"].(map[string]any)
	if args["session_id"] != forged {
		t.Errorf("raw arguments must still record what the agent sent, got %v", args)
	}
}

// A well-formed handle is still trusted verbatim, so the rejection above is a
// genuine discriminator rather than a blanket refusal.
func TestValidSuppliedSessionIsStillTrusted(t *testing.T) {
	h := newSpyHarness(t, nil)
	want := sid("parent")
	_, evt := callOnce(t, h, "add_todo", map[string]any{
		"title": "hi", "session_id": want, "context": "echoing the issued handle",
	})
	if evt.GetSessionId() != want {
		t.Errorf("sessionId = %q, want %q", evt.GetSessionId(), want)
	}
	if got := sessionSourceTag(t, evt); got != "supplied" {
		t.Errorf("session source tag = %q, want supplied", got)
	}
}

// TestCustomerOwnedSessionParamIsForeign pins the collision case end to end:
// the customer's value reaches their handler untouched, AgentCat publishes
// sessionless, and the agent is told nothing about a parameter that is not
// AgentCat's to speak about.
func TestCustomerOwnedSessionParamIsForeign(t *testing.T) {
	var received []string
	h := newSpyHarnessOn(t, newOwnSessionServer(&received), nil, "proj_test")

	// A tools/list is what records the collision in the registries.
	if _, err := h.Client.ListTools(context.Background(), mcp.ListToolsRequest{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	const customerValue = "CUSTOMER-VALUE-123"
	res, evt := callOnce(t, h, "echo", map[string]any{"text": "hi", "session_id": customerValue})

	if len(received) != 1 || received[0] != customerValue {
		t.Errorf("the customer's session_id must reach their handler untouched, got %v", received)
	}
	if evt.SessionId.Get() != nil {
		t.Errorf("sessionId = %q; a customer-owned parameter is never adopted", evt.GetSessionId())
	}
	if got := sessionSourceTag(t, evt); got != "foreign" {
		t.Errorf("session source tag = %q, want foreign", got)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "[session_id") || strings.Contains(string(raw), "mcp_session") {
		t.Errorf("AgentCat must say nothing about a parameter that is not ours: %s", raw)
	}
}

// The same tool with NO session_id at all is still foreign — ownership is a
// property of the tool, not of whether a value happened to arrive.
func TestCustomerOwnedSessionParamIsForeignWithoutAValue(t *testing.T) {
	var received []string
	h := newSpyHarnessOn(t, newOwnSessionServer(&received), nil, "proj_test")
	if _, err := h.Client.ListTools(context.Background(), mcp.ListToolsRequest{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	_, evt := callOnce(t, h, "echo", map[string]any{"text": "hi"})
	if evt.SessionId.Get() != nil {
		t.Errorf("sessionId = %q, want sessionless", evt.GetSessionId())
	}
	if got := sessionSourceTag(t, evt); got != "foreign" {
		t.Errorf("session source tag = %q, want foreign", got)
	}
}

// TestHookModeBeatsForeign pins the branch order. A hook-mode server injects
// session_id into nothing, so ownership is false for EVERY tool; if ownership
// were tested first, hook mode would collapse to foreign everywhere.
func TestHookModeBeatsForeign(t *testing.T) {
	opts := DefaultOptions()
	opts.ResolveSessionID = func(ctx context.Context, request mcp.CallToolRequest) (string, error) {
		return "corr-7", nil
	}
	var received []string
	h := newSpyHarnessOn(t, newOwnSessionServer(&received), opts, "proj_test")
	if _, err := h.Client.ListTools(context.Background(), mcp.ListToolsRequest{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	_, evt := callOnce(t, h, "echo", map[string]any{"text": "hi", "session_id": "CUSTOMER-VALUE-123"})
	if got := sessionSourceTag(t, evt); got != "hook" {
		t.Errorf("session source tag = %q, want hook", got)
	}
	if want := agentcat.DeriveSessionID("corr-7", "proj_test"); evt.GetSessionId() != want {
		t.Errorf("sessionId = %q, want the derived %q", evt.GetSessionId(), want)
	}
}

// TestFailurePathValidatesToo pins that the error hooks — which re-resolve
// handles without the middleware's record — apply the same trust rules. A
// forged handle on a failed call must not slip into Event.SessionId just
// because the failure path took a different route to resolution.
func TestFailurePathValidatesToo(t *testing.T) {
	h := newSpyHarnessOn(t, newFailureServer(t), nil, "proj_test")

	events := callFailing(t, h, "no_such_tool", map[string]any{"session_id": "not-a-real-id"})
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(events))
	}
	if events[0].SessionId.Get() != nil {
		t.Errorf("sessionId = %q; the failure path must validate too", events[0].GetSessionId())
	}
	if got := sessionSourceTag(t, events[0]); got != "invalid" {
		t.Errorf("session source tag = %q, want invalid", got)
	}
}

// TestInvalidCorrectionReachesTheAgentOnly pins the correction's wire-only
// discipline: the agent must be told its handle was not recognized, and the
// published event must carry the customer's original response without it.
//
// This is a genuine regression risk. undecorateResult recognises AgentCat's
// own mint-back by scanning for the ses_ value it names — and the correction
// names none, so before invalidMintBack existed it would have survived into
// the event. Driven through the output-validation replacement path, which is
// where mcp-go swaps the result after the middleware ran and so forces
// undecorateResult to actually run.
func TestInvalidCorrectionReachesTheAgentOnly(t *testing.T) {
	h := newSpyHarness(t, nil)

	res, evt := callOnce(t, h, "add_todo", map[string]any{
		"title": "hi", "session_id": "not-a-real-id", "context": "forged handle",
	})

	// The agent is corrected, on the wire.
	var corrected bool
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok && strings.Contains(tc.Text, "[session_id unrecognized") {
			corrected = true
		}
	}
	if !corrected {
		t.Errorf("the agent was not corrected: %+v", res.Content)
	}

	// The event carries the customer's original response only.
	resp := fmt.Sprint(evt.Response)
	if strings.Contains(resp, "unrecognized") || strings.Contains(resp, "[session_id") {
		t.Errorf("the correction leaked into the published event: %s", resp)
	}
}

// TestUndecorateStripsTheCorrection exercises the undecoration path directly,
// because the middleware's normal route captures the result BEFORE decorating
// and so would pass even if undecoration were broken.
func TestUndecorateStripsTheCorrection(t *testing.T) {
	correction := agentcat.BuildMintBackText(
		agentcat.SessionResolution{Source: agentcat.SessionSourceInvalid})

	decorated := &mcp.CallToolResult{Content: []mcp.Content{
		mcp.NewTextContent(correction),
		mcp.NewTextContent("the customer's own answer"),
	}}
	got := undecorateResult(decorated)
	if len(got.Content) != 1 {
		t.Fatalf("undecoration left %d blocks, want 1: %+v", len(got.Content), got.Content)
	}
	if tc, ok := got.Content[0].(mcp.TextContent); !ok || tc.Text != "the customer's own answer" {
		t.Errorf("undecoration removed the wrong block: %+v", got.Content[0])
	}

	// A customer block that merely mentions the phrase is NOT ours to remove.
	customer := &mcp.CallToolResult{Content: []mcp.Content{
		mcp.NewTextContent("my docs explain that session_id unrecognized means retry"),
	}}
	if len(undecorateResult(customer).Content) != 1 {
		t.Error("undecoration must only remove blocks it can rebuild byte-for-byte")
	}
}

// TestUndecorateStripsMintedDecorationAndMirror pins the full round-trip for
// the decoration decorateResult writes today: the minted block at the FIRST
// content position and the {session_id, agent_id, status} mirror. Both must be
// rebuilt and removed so the published event carries the customer's original
// result; a mirror this SDK could not have built must survive untouched.
func TestUndecorateStripsMintedDecorationAndMirror(t *testing.T) {
	id := sid("undecorate")
	minted := agentcat.BuildMintBackText(
		agentcat.SessionResolution{SessionID: id, Source: agentcat.SessionSourceMinted})

	decorated := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(minted),
			mcp.NewTextContent("the customer's own answer"),
		},
		StructuredContent: map[string]any{
			"text": "payload",
			"mcp_session": map[string]any{
				"session_id": id,
				"agent_id":   "opus|cc|k3n9x",
				"status":     "issued",
			},
		},
	}

	got := undecorateResult(decorated)
	if len(got.Content) != 1 {
		t.Fatalf("undecoration left %d blocks, want 1: %+v", len(got.Content), got.Content)
	}
	if tc, ok := got.Content[0].(mcp.TextContent); !ok || tc.Text != "the customer's own answer" {
		t.Errorf("undecoration removed the wrong block: %+v", got.Content[0])
	}
	sc, ok := got.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %T, want a JSON object", got.StructuredContent)
	}
	if _, has := sc["mcp_session"]; has {
		t.Errorf("the mirror must be removed: %v", sc)
	}
	if sc["text"] != "payload" {
		t.Errorf("customer structured payload must survive: %v", sc)
	}

	// The hook/foreign shape — agent_id alone — is ours too.
	agentOnly := &mcp.CallToolResult{StructuredContent: map[string]any{
		"mcp_session": map[string]any{"agent_id": "opus|cc|k3n9x"},
	}}
	if sc, ok := undecorateResult(agentOnly).StructuredContent.(map[string]any); !ok || sc["mcp_session"] != nil {
		t.Errorf("the agent-only mirror must be removed: %v", sc)
	}

	// A near-miss this SDK never builds (issued session named with the wrong
	// status) is not provably ours and must be left alone.
	nearMiss := &mcp.CallToolResult{StructuredContent: map[string]any{
		"mcp_session": map[string]any{"session_id": id, "status": "confirmed"},
	}}
	if sc, ok := undecorateResult(nearMiss).StructuredContent.(map[string]any); !ok || sc["mcp_session"] == nil {
		t.Error("a mirror this SDK could not have built must survive undecoration")
	}
}

// TestCollisionIsReportedFromARealList pins the wiring: the ERROR must reach
// the log from an actual tools/list, not just from a direct call to the
// reporting helper. The engine records the collision as data and the list path
// is what reports it, so a missing call site here would be silent.
func TestCollisionIsReportedFromARealList(t *testing.T) {
	var received []string
	h := newSpyHarnessOn(t, newOwnSessionServer(&received), nil, "proj_test")

	for range 3 {
		if _, err := h.Client.ListTools(context.Background(), mcp.ListToolsRequest{}); err != nil {
			t.Fatalf("ListTools: %v", err)
		}
	}

	instance := agentcat.GetInstance(h.Server)
	if instance == nil {
		t.Fatal("server is not tracked")
	}
	reg := instance.Registries.Load()
	if reg == nil {
		t.Fatal("no registries after three lists")
	}
	if !slices.Contains(reg.CustomerOwnedParams["echo"], "session_id") {
		t.Errorf("the list path must record the collision: %v", reg.CustomerOwnedParams)
	}
	// Reporting is idempotent per instance: a fourth list says nothing new.
	if instance.ClaimCollisionReport("echo") {
		t.Error("three lists must have already reported this tool exactly once")
	}
}
