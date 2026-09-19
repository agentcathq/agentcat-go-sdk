package officialsdk

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	agentcat "go.agentcat.com/sdk/v2"
)

type probeArgs struct {
	Text string `json:"text"`
}

type probeResult struct {
	Padding string `json:"padding"`
}

// Vectors: {"text":"hi there"} is 19 bytes -> 6 tokens; "hello world" is 11
// bytes -> 4. The structured mirror of the result must not count.
func TestToolCallEventsCarryTokenEstimates(t *testing.T) {
	serverImpl := &mcp.Implementation{Name: "tokens-server", Version: "1.0.0"}
	server := mcp.NewServer(serverImpl, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "token_probe", Description: "fixed reply"},
		func(ctx context.Context, req *mcp.CallToolRequest, args probeArgs) (*mcp.CallToolResult, probeResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "hello world"}}},
				probeResult{Padding: "yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"}, nil
		})

	opts := DefaultOptions()
	opts.DisableToolCallContext = true
	opts.DisableReportMissing = true
	mock := &mockPublisher{}
	instance := &agentcat.AgentCatInstance{
		ProjectID: "proj_test",
		Options: &agentcat.Options{
			DisableReportMissing:   opts.DisableReportMissing,
			DisableToolCallContext: opts.DisableToolCallContext,
		},
	}
	agentcat.RegisterServer(server, instance)
	server.AddReceivingMiddleware(newTrackingMiddleware(server, "proj_test", opts, mock.publish, serverImpl))
	t.Cleanup(func() { agentcat.UnregisterServer(server) })

	cs := connectClient(t, server)
	_, evt := callToolOn(t, cs, mock, "token_probe", map[string]any{"text": "hi there"})

	if evt.InputTokens == nil || *evt.InputTokens != 6 {
		t.Errorf("InputTokens = %v, want 6", evt.InputTokens)
	}
	if evt.OutputTokens == nil || *evt.OutputTokens != 4 {
		t.Errorf("OutputTokens = %v, want 4", evt.OutputTokens)
	}
}

// F1: a structured-only result (no Content blocks) must still record an
// empty "content" list on the event, so tokens.OutputTokens sees a list
// rather than falling back to the whole response — which would count the
// envelope keys too — and, seeing the list is empty, counts the compact JSON
// of StructuredContent instead of 0.
//
// A typed mcp.AddTool[In, Out] handler cannot produce this case: when the
// handler leaves Content nil, go-sdk synthesizes a JSON-text content block
// from the structured output before this SDK ever sees the result (go-sdk
// mcp/server.go v1.7.0 around line 935, "If the Content field isn't being
// used, return the serialized JSON in a TextContent block"). The low-level,
// non-generic (*mcp.Server).AddTool handler below skips that synthesis —
// but go-sdk still refuses to hand back a nil Content: (*Server).callTool
// normalizes it to a non-nil, EMPTY []mcp.Content{} (go-sdk mcp/server.go
// v1.7.0 around line 968, "avoid \"null\"") before the result reaches this
// SDK's middleware. So the empty-Content case is real and reachable, just
// never as a raw `nil` — this test exercises it end-to-end rather than
// constructing `Content: []mcp.Content{}` by hand.
func TestTokenEstimatesStructuredOnlyResultCountsStructuredContent(t *testing.T) {
	serverImpl := &mcp.Implementation{Name: "tokens-structured", Version: "1.0.0"}
	server := mcp.NewServer(serverImpl, nil)
	server.AddTool(
		&mcp.Tool{Name: "structured_probe", Description: "structured-only reply", InputSchema: map[string]any{"type": "object"}},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				StructuredContent: map[string]any{"big": strings.Repeat("y", 50)},
			}, nil
		},
	)

	opts := DefaultOptions()
	opts.DisableToolCallContext = true
	opts.DisableReportMissing = true
	mock := &mockPublisher{}
	instance := &agentcat.AgentCatInstance{
		ProjectID: "proj_test",
		Options: &agentcat.Options{
			DisableReportMissing:   opts.DisableReportMissing,
			DisableToolCallContext: opts.DisableToolCallContext,
		},
	}
	agentcat.RegisterServer(server, instance)
	server.AddReceivingMiddleware(newTrackingMiddleware(server, "proj_test", opts, mock.publish, serverImpl))
	t.Cleanup(func() { agentcat.UnregisterServer(server) })

	cs := connectClient(t, server)
	_, evt := callToolOn(t, cs, mock, "structured_probe", map[string]any{})

	// {"big":"` (8 bytes) + 50 y's + `"}` (2 bytes) = 60 bytes -> ceil(60/3.5) = 18.
	if evt.OutputTokens == nil || *evt.OutputTokens != 18 {
		t.Errorf("OutputTokens = %v, want 18", evt.OutputTokens)
	}
	content, ok := evt.Response["content"].([]any)
	if !ok || len(content) != 0 {
		t.Errorf("Response[\"content\"] = %#v, want []any{}", evt.Response["content"])
	}
}
