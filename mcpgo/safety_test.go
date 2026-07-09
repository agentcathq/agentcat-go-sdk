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
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
)

// unhashableErr is an error whose dynamic type cannot be used as a map key
// (slice field, value receiver). Error capture must tolerate it: hooks run
// synchronously inside mcp-go's request handling, so a panic here would crash
// the customer's server.
type unhashableErr struct{ parts []string }

func (e unhashableErr) Error() string { return "tool exploded" }

// setupSafetyServer builds a minimal tracked server with hostile test tools,
// publishing into a mockPublisher, connected via an in-process client.
func setupSafetyServer(t *testing.T, opts *Options) (*client.Client, *mockPublisher) {
	t.Helper()

	mcpServer := server.NewMCPServer("safety-server", "1.0.0",
		server.WithToolCapabilities(true))

	mcpServer.AddTool(
		mcp.NewTool("unhashable_error", mcp.WithDescription("returns an unhashable error")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, unhashableErr{parts: []string{"boom"}}
		},
	)
	mcpServer.AddTool(
		mcp.NewTool("ok_tool", mcp.WithDescription("succeeds")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	)

	hooks := &server.Hooks{}
	server.WithHooks(hooks)(mcpServer)

	mock := &mockPublisher{}
	sessionMap := addTracingToHooks(hooks, opts, mock.publish)
	t.Cleanup(sessionMap.Stop)

	instance := &agentcat.AgentCatInstance{
		ProjectID: "test_project",
		Options:   &agentcat.Options{},
		ServerRef: mcpServer,
		SessionID: agentcat.NewSessionID(),
	}
	agentcat.RegisterServer(mcpServer, instance)
	t.Cleanup(func() { unregisterServer(mcpServer) })

	// Use a real HTTP transport so hooks see a client session (the in-process
	// transport does not attach one).
	httpServer := server.NewTestStreamableHTTPServer(mcpServer)
	t.Cleanup(httpServer.Close)

	mcpClient, err := client.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		t.Fatalf("NewStreamableHttpClient failed: %v", err)
	}

	ctx := context.Background()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	t.Cleanup(func() { mcpClient.Close() })

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "safety-client", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	return mcpClient, mock
}

func callSafetyTool(t *testing.T, c *client.Client, name string) (*mcp.CallToolResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	return c.CallTool(ctx, req)
}

// TestHostileToolErrors_DoNotCrashServer drives a tool whose error has an
// unhashable dynamic type through the OnError hook over a real transport
// (error capture uses errors as map keys for cycle detection, which panics on
// unhashable types). The invariant: the customer's server must survive, and
// subsequent requests must still work.
//
// Note: an error whose Error() method itself panics crashes inside mcp-go's
// own response marshaling before AgentCat's hooks run, so that case is out of
// the SDK's control (covered at unit level in internal/exceptions).
func TestHostileToolErrors_DoNotCrashServer(t *testing.T) {
	mcpClient, mock := setupSafetyServer(t, DefaultOptions())

	// Hostile tool: the call errors (expected), the process survives.
	_, _ = callSafetyTool(t, mcpClient, "unhashable_error")

	// The server must still serve requests.
	result, err := callSafetyTool(t, mcpClient, "ok_tool")
	if err != nil {
		t.Fatalf("server did not survive hostile errors: %v", err)
	}
	if resultText(result) != "ok" {
		t.Errorf("unexpected result after hostile errors: %q", resultText(result))
	}

	// The unhashable error's event should still be captured with its message
	// (mcp-go wraps it, so match on the substring).
	events := mock.waitForEvents(3, 3*time.Second)
	var sawUnhashable bool
	for _, evt := range events {
		if evt.Error != nil {
			if msg, _ := evt.Error["message"].(string); strings.Contains(msg, "tool exploded") {
				sawUnhashable = true
			}
		}
	}
	if !sawUnhashable {
		t.Error("expected an error event carrying the unhashable error's message")
	}
}

// TestConcurrentIdentifyChanges_NoRace hammers tool calls while the Identify
// callback keeps returning different identities with UserData maps, verifying
// (under -race) that identity merge, session mutation, and identify-event
// publishing are properly synchronized.
func TestConcurrentIdentifyChanges_NoRace(t *testing.T) {
	var n atomic.Int64
	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.Identify = func(ctx context.Context, request any) *UserIdentity {
		i := n.Add(1)
		return &UserIdentity{
			UserID:   fmt.Sprintf("user-%d", i%4),
			UserName: "racer",
			UserData: map[string]any{"call": i, "nested": map[string]any{"k": i}},
		}
	}

	mcpClient, mock := setupSafetyServer(t, opts)

	const workers = 8
	const callsPerWorker = 10
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range callsPerWorker {
				_, _ = callSafetyTool(t, mcpClient, "ok_tool")
			}
		}()
	}
	wg.Wait()

	// Every call returns a non-nil identity, so every tool call publishes an
	// identify event alongside its tools/call event (no dedup).
	events := mock.waitForEvents(2*workers*callsPerWorker, 5*time.Second)
	var identifies int
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == "agentcat:identify" {
			identifies++
		}
	}
	if identifies < workers*callsPerWorker {
		t.Errorf("expected at least %d identify events (one per tool call), got %d",
			workers*callsPerWorker, identifies)
	}
}

// TestTrackShutdown_DoubleAndConcurrent verifies the adapter shutdown function
// and the package-level Shutdown are safe to call repeatedly and concurrently.
func TestTrackShutdown_DoubleAndConcurrent(t *testing.T) {
	mcpServer := server.NewMCPServer("shutdown-server", "1.0.0")
	shutdownFn, err := Track(mcpServer, "test_project", DefaultOptions())
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	t.Cleanup(func() { unregisterServer(mcpServer) })

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = shutdownFn(ctx)
			_ = Shutdown(ctx)
		}()
	}
	wg.Wait()

	// A second sequential round must also be safe.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := shutdownFn(ctx); err != nil {
		t.Errorf("second shutdownFn call errored: %v", err)
	}
	_ = Shutdown(ctx)
}
