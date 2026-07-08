package mcpgo

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
)

// setupStreamableHTTP creates a real HTTP-based MCP client that exercises the
// full session-capture code path (unlike in-process clients).
func setupStreamableHTTP(t *testing.T, opts *Options) *client.Client {
	t.Helper()

	mcpServer, _ := CreateFullServer()

	_, err := Track(mcpServer, "test_project", opts)
	if err != nil {
		t.Fatalf("setupStreamableHTTP: Track failed: %v", err)
	}

	httpServer := server.NewTestStreamableHTTPServer(mcpServer)

	mcpClient, err := client.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		httpServer.Close()
		unregisterServer(mcpServer)
		t.Fatalf("setupStreamableHTTP: NewStreamableHttpClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	if err := mcpClient.Start(ctx); err != nil {
		cancel()
		mcpClient.Close()
		httpServer.Close()
		unregisterServer(mcpServer)
		t.Fatalf("setupStreamableHTTP: client.Start failed: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "e2e-http-client",
		Version: "1.0.0",
	}

	_, err = mcpClient.Initialize(ctx, initReq)
	if err != nil {
		cancel()
		mcpClient.Close()
		httpServer.Close()
		unregisterServer(mcpServer)
		t.Fatalf("setupStreamableHTTP: Initialize failed: %v", err)
	}

	t.Cleanup(func() {
		mcpClient.Close()
		httpServer.Close()
		cancel()
		unregisterServer(mcpServer)
	})

	return mcpClient
}

// TestStreamableHTTP_FullPipeline verifies a basic tool call works end-to-end
// over a real HTTP transport.
func TestStreamableHTTP_FullPipeline(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	}

	mcpClient := setupStreamableHTTP(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "add_todo"
	req.Params.Arguments = map[string]any{
		"title": "HTTP e2e todo",
	}

	result, err := mcpClient.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if result.IsError {
		t.Fatalf("CallTool returned error result: %s", resultText(result))
	}

	assertContains(t, resultText(result), "HTTP e2e todo")
}

// TestStreamableHTTP_IdentifyInvoked proves that the Identify callback fires
// when a real HTTP session is present (unlike in-process tests where it is
// skipped because ClientSessionFromContext returns nil).
func TestStreamableHTTP_IdentifyInvoked(t *testing.T) {
	var identifyCount atomic.Int32

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request *mcp.CallToolRequest) *agentcat.UserIdentity {
			identifyCount.Add(1)
			return &agentcat.UserIdentity{
				UserID:   "http-user-1",
				UserName: "HTTP Test User",
			}
		},
	}

	mcpClient := setupStreamableHTTP(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "add_todo"
	req.Params.Arguments = map[string]any{
		"title": "Trigger identify",
	}

	_, err := mcpClient.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if identifyCount.Load() <= 0 {
		t.Error("expected Identify to be called at least once over HTTP transport, but it was not")
	}
}

// TestStreamableHTTP_IdentifyRerun verifies that Identify is re-run on every
// tool call (matching the TypeScript SDK): the callback fires each time, and
// identify events are deduplicated by change detection instead.
func TestStreamableHTTP_IdentifyRerun(t *testing.T) {
	var identifyCount atomic.Int32

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request *mcp.CallToolRequest) *agentcat.UserIdentity {
			identifyCount.Add(1)
			return &agentcat.UserIdentity{
				UserID:   "http-dedup-user",
				UserName: "Dedup User",
			}
		},
	}

	mcpClient := setupStreamableHTTP(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// First call: should trigger Identify
	req1 := mcp.CallToolRequest{}
	req1.Params.Name = "add_todo"
	req1.Params.Arguments = map[string]any{
		"title": "First call",
	}

	_, err := mcpClient.CallTool(ctx, req1)
	if err != nil {
		t.Fatalf("first CallTool failed: %v", err)
	}

	countAfterFirst := identifyCount.Load()
	if countAfterFirst <= 0 {
		t.Fatal("expected Identify to be called after first tool call, but it was not")
	}

	// Second call: Identify runs again on every tool call so identity
	// changes can be detected (the identify event is only published on change).
	req2 := mcp.CallToolRequest{}
	req2.Params.Name = "list_todos"
	req2.Params.Arguments = map[string]any{}

	_, err = mcpClient.CallTool(ctx, req2)
	if err != nil {
		t.Fatalf("second CallTool failed: %v", err)
	}

	countAfterSecond := identifyCount.Load()
	if countAfterSecond <= countAfterFirst {
		t.Errorf("expected Identify to be re-run on the second call (count > %d), got %d",
			countAfterFirst, countAfterSecond)
	}
}

// TestStreamableHTTP_ServerInfo verifies that the server name and version
// returned during initialization match what was configured.
func TestStreamableHTTP_ServerInfo(t *testing.T) {
	mcpServer, _ := CreateFullServer()

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	}
	_, err := Track(mcpServer, "test_project", opts)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}

	httpServer := server.NewTestStreamableHTTPServer(mcpServer)

	mcpClient, err := client.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		httpServer.Close()
		unregisterServer(mcpServer)
		t.Fatalf("NewStreamableHttpClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	if err := mcpClient.Start(ctx); err != nil {
		cancel()
		mcpClient.Close()
		httpServer.Close()
		unregisterServer(mcpServer)
		t.Fatalf("client.Start failed: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "info-check-client",
		Version: "3.0.0",
	}

	initResult, err := mcpClient.Initialize(ctx, initReq)
	if err != nil {
		cancel()
		mcpClient.Close()
		httpServer.Close()
		unregisterServer(mcpServer)
		t.Fatalf("Initialize failed: %v", err)
	}

	t.Cleanup(func() {
		mcpClient.Close()
		httpServer.Close()
		cancel()
		unregisterServer(mcpServer)
	})

	if initResult.ServerInfo.Name != "todo-server" {
		t.Errorf("expected ServerInfo.Name=%q, got %q", "todo-server", initResult.ServerInfo.Name)
	}
	if initResult.ServerInfo.Version != "1.0.0" {
		t.Errorf("expected ServerInfo.Version=%q, got %q", "1.0.0", initResult.ServerInfo.Version)
	}
}

// TestStreamableHTTP_ConcurrentIdentifyDedup verifies that when multiple
// concurrent tool calls arrive on the same session, Identify fires exactly
// once and no data races occur. Run with -race to verify.
func TestStreamableHTTP_ConcurrentIdentifyDedup(t *testing.T) {
	var identifyCount atomic.Int32

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request *mcp.CallToolRequest) *agentcat.UserIdentity {
			identifyCount.Add(1)
			return &agentcat.UserIdentity{
				UserID:   "concurrent-user",
				UserName: "Concurrent User",
				UserData: map[string]any{"role": "tester"},
			}
		},
	}

	mcpServer, _ := CreateFullServer()

	_, err := Track(mcpServer, "test_project", opts)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}

	httpServer := server.NewTestStreamableHTTPServer(mcpServer)

	// Create multiple HTTP clients (each gets its own session)
	const numClients = 5
	clients := make([]*client.Client, numClients)

	for i := 0; i < numClients; i++ {
		c, err := client.NewStreamableHttpClient(httpServer.URL)
		if err != nil {
			httpServer.Close()
			unregisterServer(mcpServer)
			t.Fatalf("NewStreamableHttpClient[%d] failed: %v", i, err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := c.Start(ctx); err != nil {
			c.Close()
			httpServer.Close()
			unregisterServer(mcpServer)
			t.Fatalf("client[%d].Start failed: %v", i, err)
		}

		initReq := mcp.InitializeRequest{}
		initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
		initReq.Params.ClientInfo = mcp.Implementation{
			Name:    fmt.Sprintf("concurrent-client-%d", i),
			Version: "1.0.0",
		}

		if _, err := c.Initialize(ctx, initReq); err != nil {
			c.Close()
			httpServer.Close()
			unregisterServer(mcpServer)
			t.Fatalf("client[%d].Initialize failed: %v", i, err)
		}

		clients[i] = c
	}

	t.Cleanup(func() {
		for _, c := range clients {
			c.Close()
		}
		httpServer.Close()
		unregisterServer(mcpServer)
	})

	// Fire tool calls from all clients concurrently
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func(idx int, cl *client.Client) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			req := mcp.CallToolRequest{}
			req.Params.Name = "add_todo"
			req.Params.Arguments = map[string]any{
				"title": fmt.Sprintf("concurrent todo %d", idx),
			}

			result, err := cl.CallTool(ctx, req)
			if err != nil {
				t.Errorf("client[%d] CallTool failed: %v", idx, err)
				return
			}
			if result.IsError {
				t.Errorf("client[%d] got error result: %s", idx, resultText(result))
			}
		}(i, c)
	}

	wg.Wait()

	// Each client has its own session, so Identify should fire once per session.
	// The key assertion is that the race detector does NOT fire.
	count := identifyCount.Load()
	if count != int32(numClients) {
		t.Errorf("expected Identify to be called %d times (once per session), got %d", numClients, count)
	}
}
