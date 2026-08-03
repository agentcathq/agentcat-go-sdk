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
	agentcat "go.agentcat.com/sdk/v2"
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
		Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
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
// captured request: the callback fires each time, and an identify event is
// published every time it returns a non-nil identity (no dedup).
func TestStreamableHTTP_IdentifyRerun(t *testing.T) {
	var identifyCount atomic.Int32

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
			identifyCount.Add(1)
			return &agentcat.UserIdentity{
				UserID:   "http-rerun-user",
				UserName: "Rerun User",
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

	// Second call: Identify runs again on every captured request; an identify
	// event is published each time it returns a non-nil identity.
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

// callSpyTool drives a tool call through a spy-backed client.
func callSpyTool(t *testing.T, c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	result, err := c.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool(%q): %v", name, err)
	}
	return result
}

// TestStreamableHTTP_FullSessionPublishesOnlyToolCalls drives the whole MCP
// surface the fixture exposes over a real HTTP transport and pins the v2 rule:
// tool calls are the only thing that publishes.
func TestStreamableHTTP_FullSessionPublishesOnlyToolCalls(t *testing.T) {
	mcpClient, mock := setupSpyHTTP(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	readReq := mcp.ReadResourceRequest{}
	readReq.Params.URI = "todo://about"
	if _, err := mcpClient.ReadResource(ctx, readReq); err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	promptReq := mcp.GetPromptRequest{}
	promptReq.Params.Name = "summarize_todos"
	promptReq.Params.Arguments = map[string]string{"style": "brief"}
	if _, err := mcpClient.GetPrompt(ctx, promptReq); err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	for _, title := range []string{"one", "two"} {
		callSpyTool(t, mcpClient, "add_todo", map[string]any{"title": title})
	}

	mock.waitForEvents(2, 3*time.Second)
	settleForAbsentEvents()

	events := mock.getEvents()
	for _, e := range events {
		if e.EventType == nil || *e.EventType != "mcp:tools/call" {
			t.Errorf("unexpected event type %v — v2 publishes only tool calls", e.EventType)
		}
	}
	if got := len(events); got != 2 {
		t.Errorf("expected exactly 2 events for 2 tool calls, got %d (%v)", got, eventTypes(events))
	}
}

// TestStreamableHTTP_SuppliedSessionRoundTrips pins that a session_id supplied by the
// agent survives a real HTTP hop: every call carrying it is attributed to it,
// and none of them re-mints.
func TestStreamableHTTP_SuppliedSessionRoundTrips(t *testing.T) {
	mcpClient, mock := setupSpyHTTP(t, nil)

	session := sid("http_supplied")
	for _, title := range []string{"first", "second"} {
		result := callSpyTool(t, mcpClient, "add_todo", map[string]any{"title": title, "session_id": session})
		for _, c := range result.Content {
			if tc, ok := c.(mcp.TextContent); ok && strings.Contains(tc.Text, "session_id issued") {
				t.Errorf("call %s re-minted a session the agent already supplied", title)
			}
		}
	}

	mock.waitForEvents(2, 3*time.Second)
	events := filterEvents(mock.getEvents(), "mcp:tools/call")
	if len(events) != 2 {
		t.Fatalf("expected 2 tool-call events, got %d", len(events))
	}
	for i, evt := range events {
		if evt.GetSessionId() != session {
			t.Errorf("event %d attributed to %q, want %q", i, evt.GetSessionId(), session)
		}
		if evt.Tags == nil || (*evt.Tags)[agentcat.TagSessionSource] != string(agentcat.SessionSourceSupplied) {
			t.Errorf("event %d session source tag = %v, want %q", i, evt.Tags, agentcat.SessionSourceSupplied)
		}
	}
}

// TestStreamableHTTP_IdentityStampsEveryCallNoIdentifyEvents pins the v2
// identify contract over a real transport: the callback runs on every tool
// call, its result stamps that call's event, and no separate identify event is
// ever published.
func TestStreamableHTTP_IdentityStampsEveryCallNoIdentifyEvents(t *testing.T) {
	var identifyCount atomic.Int32
	opts := DefaultOptions()
	opts.Identify = func(ctx context.Context, request any) *agentcat.UserIdentity {
		identifyCount.Add(1)
		return &agentcat.UserIdentity{
			UserID:   "http-actor",
			UserName: "HTTP Actor",
			UserData: map[string]any{"plan": "pro"},
		}
	}

	mcpClient, mock := setupSpyHTTP(t, opts)
	for _, title := range []string{"first", "second"} {
		callSpyTool(t, mcpClient, "add_todo", map[string]any{"title": title})
	}

	mock.waitForEvents(2, 3*time.Second)
	settleForAbsentEvents()

	events := filterEvents(mock.getEvents(), "mcp:tools/call")
	if len(events) != 2 {
		t.Fatalf("expected 2 tool-call events, got %d", len(events))
	}
	if got := identifyCount.Load(); got != 2 {
		t.Errorf("Identify ran %d times, want once per tool call (2)", got)
	}
	for i, evt := range events {
		if evt.IdentifyActorGivenId == nil || *evt.IdentifyActorGivenId != "http-actor" {
			t.Errorf("event %d not stamped with the actor: %v", i, evt.IdentifyActorGivenId)
		}
		if evt.IdentifyData["plan"] != "pro" {
			t.Errorf("event %d not stamped with identify data: %v", i, evt.IdentifyData)
		}
	}
	if identifies := filterEvents(mock.getEvents(), "agentcat:identify"); len(identifies) != 0 {
		t.Errorf("v2 publishes no agentcat:identify events, got %d", len(identifies))
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

// TestStreamableHTTP_ConcurrentIdentifyNoRace verifies that when concurrent
// tool calls arrive from multiple sessions, Identify fires once per tool call
// and no data races occur. Run with -race to verify.
func TestStreamableHTTP_ConcurrentIdentifyNoRace(t *testing.T) {
	var identifyCount atomic.Int32

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
			// Gate to tool calls so the count below stays deterministic
			// (identify also runs for initialize and other captured methods).
			if _, ok := request.(*mcp.CallToolRequest); !ok {
				return nil
			}
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

	// Each client makes exactly one tool call, so the gated callback returns
	// an identity exactly numClients times. The key assertion is that the
	// race detector does NOT fire.
	count := identifyCount.Load()
	if count != int32(numClients) {
		t.Errorf("expected Identify to be called %d times (once per tool call), got %d", numClients, count)
	}
}
