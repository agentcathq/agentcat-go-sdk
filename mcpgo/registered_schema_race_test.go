package mcpgo

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/v2"
)

// TestConcurrentSetToolsDuringListIsRaceFree pins that AgentCat's reach into
// mcp-go's private tool map is properly synchronized against mcp-go's own
// writers. SetTools REASSIGNS the map field (server/server.go:1010) — the only
// site that does — so anything reading that field outside toolsMu races with it.
//
// Nothing else in this package calls SetTools or DeleteTools, and
// concurrency_test.go only exercises parallel tools/call, so without this probe
// a green -race run cannot see the class at all.
func TestConcurrentSetToolsDuringListIsRaceFree(t *testing.T) {
	mcpServer := server.NewMCPServer("race-probe", "1.0.0",
		server.WithToolCapabilities(true),
	)
	newTool := func(name string) server.ServerTool {
		return server.ServerTool{
			Tool: mcp.NewTool(name,
				mcp.WithDescription("probe"),
				mcp.WithString("p", mcp.Description("payload")),
				mcp.WithOutputSchema[validatedOutput](),
			),
			Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{StructuredContent: map[string]any{"text": "ok"}}, nil
			},
		}
	}
	mcpServer.AddTools(newTool("alpha"), newTool("beta"))

	instance := newTestInstance(mcpServer, "proj_test", DefaultOptions())
	agentcat.RegisterServer(mcpServer, instance)
	t.Cleanup(func() { unregisterServer(mcpServer) })
	installTracking(mcpServer, instance, DefaultOptions(), (&mockPublisher{}).publish)

	const rounds = 200
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			mcpServer.SetTools(newTool("alpha"), newTool("beta"))
		}
	}()
	go func() {
		defer wg.Done()
		ctx := context.Background()
		for i := 0; i < rounds; i++ {
			mcpServer.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		}
	}()

	wg.Wait()

	// The race detector is this test's main oracle, and an oracle that only
	// reports absence is worthless if the code under test stopped running. If
	// the declaration pass ever stands down entirely — the hook guards out,
	// liveToolState reports failure, the change set is permanently empty —
	// this test would stay green and look exactly like a meaningful pass. So
	// assert the pass actually did its work.
	advertised := listToolRawJSON(t, mcpServer, "alpha")
	if !strings.Contains(advertised, `"session_id"`) {
		t.Fatalf("the advertised schema lost its injection; the race probe was exercising nothing: %s", advertised)
	}
	registered := mcpServer.ListTools()["alpha"]
	if registered == nil {
		t.Fatal("alpha is no longer registered")
	}
	declared := marshalRegisteredInputSchema(t, registered.Tool)
	props, _ := declared["properties"].(map[string]any)
	if _, has := props["session_id"]; !has {
		t.Errorf("the declaration pass never reached the registered schema; the race probe was exercising nothing: %v", declared)
	}
}
