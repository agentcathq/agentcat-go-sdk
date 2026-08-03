package officialsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// ownSessionArgs is a customer tool that declares its OWN session_id — the
// collision case. AgentCat must neither inject into it nor read it.
type ownSessionArgs struct {
	Text      string `json:"text"`
	SessionID string `json:"session_id,omitempty" jsonschema:"the customer's own correlation id"`
}

type ownSessionResult struct {
	Echoed string `json:"echoed"`
}

// trackedOwnSessionServer builds a tracked server whose single tool declares
// session_id itself, and records every session_id value that reached the
// handler so the test can prove the customer's argument survived stripping.
func trackedOwnSessionServer(t *testing.T, opts *Options) (*mcp.Server, *mockPublisher, *[]string) {
	t.Helper()

	serverImpl := &mcp.Implementation{Name: "own-session-server", Version: "1.0.0"}
	server := mcp.NewServer(serverImpl, nil)

	var received []string
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "declares its own session_id",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ownSessionArgs) (*mcp.CallToolResult, ownSessionResult, error) {
		received = append(received, args.SessionID)
		return nil, ownSessionResult{Echoed: args.Text}, nil
	})

	if opts == nil {
		opts = DefaultOptions()
	}
	mock := &mockPublisher{}
	instance := &agentcat.AgentCatInstance{
		ProjectID: "proj_test",
		Options: &agentcat.Options{
			DisableReportMissing:   opts.DisableReportMissing,
			DisableToolCallContext: opts.DisableToolCallContext,
			EnableAgentTracking:    opts.EnableAgentTracking,
		},
	}
	agentcat.RegisterServer(server, instance)
	server.AddReceivingMiddleware(newTrackingMiddleware(server, "proj_test", opts, mock.publish, serverImpl))
	t.Cleanup(func() { agentcat.UnregisterServer(server) })

	return server, mock, &received
}

func sessionSourceTag(t *testing.T, evt *agentcat.Event) string {
	t.Helper()
	if evt.Tags == nil {
		t.Fatal("event carries no tags")
	}
	return (*evt.Tags)["agentcat_session_id_source"]
}

// TestInvalidSessionPublishesSessionless is the core of the trust model: a
// session_id this server never issued is not adopted into Event.SessionId,
// which is exempt from both redaction hooks. The raw value must still reach
// the recorded arguments, where redaction CAN reach it.
func TestInvalidSessionPublishesSessionless(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, nil)
	cs := connectClient(t, server)

	const forged = "sk_live_not_a_session"
	res, evt := callToolOn(t, cs, mock, "add_todo", map[string]any{
		"title":      "hi",
		"session_id": forged,
		"context":    "trying a made-up handle",
	})

	if evt.SessionId.Get() != nil {
		t.Errorf("sessionId = %q; a value this server never issued must not be adopted", evt.GetSessionId())
	}
	if got := sessionSourceTag(t, evt); got != "invalid" {
		t.Errorf("session source tag = %q, want invalid", got)
	}
	// The forged value is still recorded where redaction can scrub it.
	args, _ := evt.Parameters["arguments"].(map[string]any)
	if args["session_id"] != forged {
		t.Errorf("raw arguments must still record what the agent sent, got %v", args)
	}
	// The agent is corrected on the wire...
	if got := lastText(t, res); !strings.Contains(got, "session_id not recognized") {
		t.Errorf("the agent was not corrected; last content = %q", got)
	}
	// ...and the correction never reaches the published event.
	if resp := fmt.Sprint(evt.Response); strings.Contains(resp, "not recognized") {
		t.Errorf("the correction leaked into the published event: %s", resp)
	}
	// It must hand out NO replacement handle.
	if strings.Contains(lastText(t, res), "session_id=ses_") {
		t.Error("the correction must not issue a replacement handle")
	}
}

// A well-formed handle is still trusted verbatim, so the rejection above is a
// genuine discriminator rather than a blanket refusal.
func TestValidSuppliedSessionIsStillTrusted(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, nil)
	cs := connectClient(t, server)

	want := sid("parent")
	_, evt := callToolOn(t, cs, mock, "add_todo", map[string]any{
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
	server, mock, received := trackedOwnSessionServer(t, nil)
	cs := connectClient(t, server)

	// A tools/list is what records the collision in the registries.
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	const customerValue = "CUSTOMER-VALUE-123"
	res, evt := callToolOn(t, cs, mock, "echo", map[string]any{
		"text": "hi", "session_id": customerValue,
	})

	if len(*received) != 1 || (*received)[0] != customerValue {
		t.Errorf("the customer's session_id must reach their handler untouched, got %v", *received)
	}
	if evt.SessionId.Get() != nil {
		t.Errorf("sessionId = %q; a customer-owned parameter is never adopted", evt.GetSessionId())
	}
	if got := sessionSourceTag(t, evt); got != "foreign" {
		t.Errorf("session source tag = %q, want foreign", got)
	}
	// Silence: no correction, no mint-back, no mirror.
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "MCP INSTRUCTIONS") || strings.Contains(string(raw), "_mcp_instructions") {
		t.Errorf("AgentCat must say nothing about a parameter that is not ours: %s", raw)
	}
}

// The same tool with NO session_id at all is still foreign — ownership is a
// property of the tool, not of whether a value happened to arrive.
func TestCustomerOwnedSessionParamIsForeignWithoutAValue(t *testing.T) {
	server, mock, _ := trackedOwnSessionServer(t, nil)
	cs := connectClient(t, server)
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	_, evt := callToolOn(t, cs, mock, "echo", map[string]any{"text": "hi"})
	if evt.SessionId.Get() != nil {
		t.Errorf("sessionId = %q, want sessionless", evt.GetSessionId())
	}
	if got := sessionSourceTag(t, evt); got != "foreign" {
		t.Errorf("session source tag = %q, want foreign", got)
	}
}

// TestCallBeforeAnyListStillValidates pins the "missing means ours" rule: a
// call that lands before discovery must still be validated, not disowned.
// Without it, every pre-listing call would silently publish foreign.
func TestCallBeforeAnyListStillValidates(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, nil)
	cs := connectClient(t, server)

	// No ListTools call: the registries are empty for this tool.
	_, evt := callToolOn(t, cs, mock, "add_todo", map[string]any{
		"title": "hi", "session_id": "nope", "context": "no discovery happened",
	})
	if got := sessionSourceTag(t, evt); got != "invalid" {
		t.Errorf("session source tag = %q, want invalid (a tool we have not listed counts as ours)", got)
	}
}

// TestHookModeBeatsForeign pins the branch order. A hook-mode server injects
// session_id into nothing, so ownership is false for EVERY tool; if ownership
// were tested first, hook mode would collapse to foreign everywhere.
func TestHookModeBeatsForeign(t *testing.T) {
	opts := DefaultOptions()
	opts.ResolveSessionID = func(ctx context.Context, req mcp.Request) (string, error) {
		return "corr-7", nil
	}
	server, mock, _ := trackedOwnSessionServer(t, opts)
	cs := connectClient(t, server)
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	_, evt := callToolOn(t, cs, mock, "echo", map[string]any{
		"text": "hi", "session_id": "CUSTOMER-VALUE-123",
	})
	if got := sessionSourceTag(t, evt); got != "hook" {
		t.Errorf("session source tag = %q, want hook", got)
	}
	if want := agentcat.DeriveSessionID("corr-7", "proj_test"); evt.GetSessionId() != want {
		t.Errorf("sessionId = %q, want the derived %q", evt.GetSessionId(), want)
	}
}

// TestForeignAndInvalidNeverShareASession is the property the whole change
// exists to protect: two sessionless calls must not end up correlated.
func TestForeignAndInvalidNeverShareASession(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, nil)
	cs := connectClient(t, server)

	_, a := callToolOn(t, cs, mock, "add_todo", map[string]any{"title": "a", "session_id": "nope", "context": "x"})
	_, b := callToolOn(t, cs, mock, "add_todo", map[string]any{"title": "b", "session_id": "also-nope", "context": "y"})

	for _, evt := range []*agentcat.Event{a, b} {
		if evt.SessionId.Get() != nil {
			t.Fatalf("expected sessionless, got %q", evt.GetSessionId())
		}
	}
	raw, _ := json.Marshal(a)
	if strings.Contains(string(raw), `"session_id":""`) {
		t.Errorf(`sessionless events must serialize session_id as null, not "": %s`, raw)
	}
}
