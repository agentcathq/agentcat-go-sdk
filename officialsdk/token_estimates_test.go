package officialsdk

import (
	"context"
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
