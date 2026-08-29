package officialsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// parallelCalls is the fan-out used by the cross-attribution tests. Every call
// carries handles unique to itself, so any state that leaks between in-flight
// calls shows up as a mismatch rather than as a race-detector report alone.
const parallelCalls = 50

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("payload %q is not an index: %v", s, err)
	}
	return n
}

// echoedPayload digs the payload the HANDLER saw out of the response recorded
// on the event. It travels with the result object, not with the request, so it
// is an independent witness of which call this event belongs to.
func echoedPayload(t *testing.T, evt *agentcat.Event) string {
	t.Helper()
	content, ok := evt.Response["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("event response has no content: %v", evt.Response)
	}
	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("response content block = %T, want object", content[0])
	}
	text, _ := block["text"].(string)
	var echoed map[string]any
	if err := json.Unmarshal([]byte(text), &echoed); err != nil {
		t.Fatalf("decode echoed args %q: %v", text, err)
	}
	payload, _ := echoed["payload"].(string)
	return payload
}

// TestParallelCallsDoNotCrossAttribute fires many concurrent tool calls over one
// real transport, each with its own session_id, agent_id, and payload, and checks
// that every published event agrees with itself on all three.
//
// The request side (recorded arguments) and the result side (recorded response)
// reach the event through different paths — the request from the middleware's
// captured pointer, the response from the result the handler returned — so a
// crossed call shows up as a mismatch, not merely as a data race.
func TestParallelCallsDoNotCrossAttribute(t *testing.T) {
	opts := DefaultOptions()
	opts.EnableAgentTracking = true
	clientSession, _, mock := setupStreamableHTTP(t, opts)

	var wg sync.WaitGroup
	for i := range parallelCalls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "echo_args",
				Arguments: map[string]any{
					"payload":    strconv.Itoa(i),
					"session_id": sid(fmt.Sprintf("task%03d", i)),
					"agent_id":   fmt.Sprintf("agent-%03d", i),
					"context":    fmt.Sprintf("intent-%03d", i),
				},
			})
			if err != nil {
				t.Errorf("call %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	events := waitForEventType(mock, "mcp:tools/call", parallelCalls, 10*time.Second)
	if len(events) != parallelCalls {
		t.Fatalf("expected %d tool-call events, got %d", parallelCalls, len(events))
	}

	seen := make(map[string]bool, parallelCalls)
	for _, evt := range events {
		args, ok := evt.Parameters["arguments"].(map[string]any)
		if !ok {
			t.Fatalf("event has no recorded arguments: %v", evt.Parameters)
		}
		payload, _ := args["payload"].(string)
		i := mustAtoi(t, payload)

		wantSession := sid(fmt.Sprintf("task%03d", i))
		if got := evt.GetSessionId(); got != wantSession {
			t.Errorf("event for payload=%s attributed to session %s, want %s", payload, got, wantSession)
		}
		wantAgent := fmt.Sprintf("agent-%03d", i)
		if evt.Tags == nil || (*evt.Tags)[agentcat.TagAgentID] != wantAgent {
			t.Errorf("event for payload=%s carries agent %v, want %s", payload, evt.Tags, wantAgent)
		}
		wantIntent := fmt.Sprintf("intent-%03d", i)
		if evt.UserIntent == nil || *evt.UserIntent != wantIntent {
			t.Errorf("event for payload=%s carries intent %v, want %s", payload, evt.UserIntent, wantIntent)
		}
		if got := echoedPayload(t, evt); got != payload {
			t.Errorf("event for payload=%s recorded the response of call %s", payload, got)
		}
		if seen[wantSession] {
			t.Errorf("session %s attributed to more than one event", wantSession)
		}
		seen[wantSession] = true
	}
	if len(seen) != parallelCalls {
		t.Errorf("expected %d distinct sessions, got %d", parallelCalls, len(seen))
	}
}

// TestParallelCallsMintDistinctSessions pins the other half: calls that supply no
// session_id each mint their own, and each mint-back names the session that call's
// event was attributed to. Minting shares no state between calls.
func TestParallelCallsMintDistinctSessions(t *testing.T) {
	clientSession, _, mock := setupStreamableHTTP(t, nil)

	type outcome struct {
		payload   string
		firstText string
	}
	results := make([]outcome, parallelCalls)

	var wg sync.WaitGroup
	for i := range parallelCalls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := strconv.Itoa(i)
			res, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "echo_args",
				Arguments: map[string]any{"payload": payload},
			})
			if err != nil {
				t.Errorf("call %d: %v", i, err)
				return
			}
			if len(res.Content) == 0 {
				t.Errorf("call %d returned no content", i)
				return
			}
			tc, ok := res.Content[0].(*mcp.TextContent)
			if !ok {
				t.Errorf("call %d: first content block is %T", i, res.Content[0])
				return
			}
			results[i] = outcome{payload: payload, firstText: tc.Text}
		}()
	}
	wg.Wait()

	events := waitForEventType(mock, "mcp:tools/call", parallelCalls, 10*time.Second)
	if len(events) != parallelCalls {
		t.Fatalf("expected %d tool-call events, got %d", parallelCalls, len(events))
	}

	sessionByPayload := make(map[string]string, parallelCalls)
	minted := make(map[string]bool, parallelCalls)
	for _, evt := range events {
		args, _ := evt.Parameters["arguments"].(map[string]any)
		payload, _ := args["payload"].(string)
		session := evt.GetSessionId()
		if !strings.HasPrefix(session, "ses_") {
			t.Errorf("call payload=%s got no minted session: %q", payload, session)
		}
		if minted[session] {
			t.Errorf("session %s was minted for more than one call", session)
		}
		minted[session] = true
		sessionByPayload[payload] = session
	}
	if len(minted) != parallelCalls {
		t.Errorf("expected %d distinct minted sessions, got %d", parallelCalls, len(minted))
	}

	// Each caller's own wire response names its own session.
	for i, got := range results {
		session, ok := sessionByPayload[got.payload]
		if !ok {
			t.Errorf("no event for call %d", i)
			continue
		}
		if want := mintBackFor(session); got.firstText != want {
			t.Errorf("call %d got mint-back %q, want %q", i, got.firstText, want)
		}
	}
}
