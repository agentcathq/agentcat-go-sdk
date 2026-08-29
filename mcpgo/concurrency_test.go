package mcpgo

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	agentcat "go.agentcat.com/sdk/v2"
)

// parallelCalls is the fan-out used by the cross-attribution tests. Every call
// carries handles unique to itself, so state that leaks between in-flight calls
// shows up as a mismatch rather than as a race-detector report alone.
const parallelCalls = 50

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("payload %q is not an index: %v", s, err)
	}
	return n
}

// newConcurrencyClient wires the handle fixtures onto a tracked server behind a
// real streamable HTTP transport, with events going to a spy publisher.
func newConcurrencyClient(t *testing.T, opts *Options) (callFunc, *mockPublisher) {
	t.Helper()
	mcpServer, _ := CreateFullServer()
	registerHandleFixtures(mcpServer)
	mcpClient, mock := setupSpyHTTPOn(t, mcpServer, opts)

	return func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
		req := mcp.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = args
		return mcpClient.CallTool(ctx, req)
	}, mock
}

type callFunc func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error)

// echoedPayload digs the payload the HANDLER saw out of the response recorded
// on the event. On this adapter the response reaches the event through the
// middleware's per-call stash while the arguments come from the after-hook's
// own request, so the two agreeing is a real cross-attribution check.
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
func TestParallelCallsDoNotCrossAttribute(t *testing.T) {
	opts := DefaultOptions()
	opts.EnableAgentTracking = true
	call, mock := newConcurrencyClient(t, opts)

	var wg sync.WaitGroup
	for i := range parallelCalls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := call(ctx, "echo_args", map[string]any{
				"payload":    strconv.Itoa(i),
				"session_id": sid(fmt.Sprintf("task%03d", i)),
				"agent_id":   fmt.Sprintf("agent-%03d", i),
				"context":    fmt.Sprintf("intent-%03d", i),
			})
			if err != nil {
				t.Errorf("call %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	mock.waitForEvents(parallelCalls, 10*time.Second)
	events := filterEvents(mock.getEvents(), "mcp:tools/call")
	if len(events) != parallelCalls {
		t.Fatalf("expected %d tool-call events, got %d (%v)",
			parallelCalls, len(events), eventTypes(mock.getEvents()))
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
// session_id each mint their own, and each caller's own wire response names the
// session its event was attributed to. Minting shares no state between calls.
func TestParallelCallsMintDistinctSessions(t *testing.T) {
	call, mock := newConcurrencyClient(t, nil)

	firstTexts := make([]string, parallelCalls)

	var wg sync.WaitGroup
	for i := range parallelCalls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			res, err := call(ctx, "echo_args", map[string]any{"payload": strconv.Itoa(i)})
			if err != nil {
				t.Errorf("call %d: %v", i, err)
				return
			}
			if len(res.Content) == 0 {
				t.Errorf("call %d returned no content", i)
				return
			}
			tc, ok := res.Content[0].(mcp.TextContent)
			if !ok {
				t.Errorf("call %d: first content block is %T", i, res.Content[0])
				return
			}
			firstTexts[i] = tc.Text
		}()
	}
	wg.Wait()

	mock.waitForEvents(parallelCalls, 10*time.Second)
	events := filterEvents(mock.getEvents(), "mcp:tools/call")
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

	for i, got := range firstTexts {
		session, ok := sessionByPayload[strconv.Itoa(i)]
		if !ok {
			t.Errorf("no event for call %d", i)
			continue
		}
		if want := mintBackFor(session); got != want {
			t.Errorf("call %d got mint-back %q, want %q", i, got, want)
		}
	}
}
