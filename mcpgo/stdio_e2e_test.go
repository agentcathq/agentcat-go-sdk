package mcpgo

import (
	"context"
	"io"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk/v2"
)

// setupStdio creates a real stdio-based MCP client connected to a tracked
// server via io.Pipe pairs. Unlike in-process clients, the stdio transport
// populates server.ClientSessionFromContext(ctx), enabling session capture
// and identify functions to fire.
func setupStdio(t *testing.T, opts *Options) *client.Client {
	t.Helper()

	mcpServer, _ := CreateFullServer()

	_, err := Track(mcpServer, "test_project", opts)
	if err != nil {
		t.Fatalf("setupStdio: Track failed: %v", err)
	}

	// Two io.Pipe pairs create bidirectional communication:
	//   client writes -> clientToServerWriter -> clientToServerReader -> server reads
	//   server writes -> serverToClientWriter -> serverToClientReader -> client reads
	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()

	stdioServer := server.NewStdioServer(mcpServer)
	stdioServer.SetErrorLogger(log.New(io.Discard, "", 0))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	// Start the server in a goroutine; capture its exit error.
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- stdioServer.Listen(ctx, clientToServerReader, serverToClientWriter)
	}()

	// Build the client side of the pipe.
	trans := transport.NewIO(serverToClientReader, clientToServerWriter, nil)

	// Explicitly start the transport: client.Start skips transport.Start for
	// *transport.Stdio (it assumes the factory already started it), but when
	// wiring manually via NewIO + NewClient we must start it ourselves so the
	// response-reading goroutine is running before we send the first request.
	if err := trans.Start(ctx); err != nil {
		cancel()
		clientToServerWriter.Close()
		serverToClientWriter.Close()
		unregisterServer(mcpServer)
		t.Fatalf("setupStdio: trans.Start failed: %v", err)
	}

	mcpClient := client.NewClient(trans)

	if err := mcpClient.Start(ctx); err != nil {
		cancel()
		clientToServerWriter.Close()
		serverToClientWriter.Close()
		unregisterServer(mcpServer)
		t.Fatalf("setupStdio: client.Start failed: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "e2e-stdio-client",
		Version: "1.0.0",
	}

	_, err = mcpClient.Initialize(ctx, initReq)
	if err != nil {
		cancel()
		clientToServerWriter.Close()
		serverToClientWriter.Close()
		unregisterServer(mcpServer)
		t.Fatalf("setupStdio: Initialize failed: %v", err)
	}

	t.Cleanup(func() {
		// 1. Cancel context -- signals the server to stop.
		cancel()
		// 2. Close pipe writers -- unblocks any blocked reads.
		clientToServerWriter.Close()
		serverToClientWriter.Close()
		// 3. Wait for the server goroutine to exit.
		<-serverDone
		// 4. Close the client and unregister the server.
		mcpClient.Close()
		unregisterServer(mcpServer)
	})

	return mcpClient
}

// setupSpyStdio is setupStdio with events routed to a mock publisher instead of
// the real one, so stdio tests can assert on the exact events AgentCat would
// send. It installs exactly what Track installs (installTracking).
func setupSpyStdio(t *testing.T, opts *Options) (*client.Client, *mockPublisher) {
	t.Helper()

	if opts == nil {
		opts = DefaultOptions()
	}

	mcpServer, _ := CreateFullServer()
	instance := newTestInstance(mcpServer, "test_project", opts)
	agentcat.RegisterServer(mcpServer, instance)

	mock := &mockPublisher{}
	installTracking(mcpServer, instance, opts, mock.publish)

	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()

	stdioServer := server.NewStdioServer(mcpServer)
	stdioServer.SetErrorLogger(log.New(io.Discard, "", 0))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- stdioServer.Listen(ctx, clientToServerReader, serverToClientWriter)
	}()

	fail := func(format string, args ...any) {
		cancel()
		clientToServerWriter.Close()
		serverToClientWriter.Close()
		unregisterServer(mcpServer)
		t.Fatalf(format, args...)
	}

	trans := transport.NewIO(serverToClientReader, clientToServerWriter, nil)
	if err := trans.Start(ctx); err != nil {
		fail("setupSpyStdio: trans.Start failed: %v", err)
	}

	mcpClient := client.NewClient(trans)
	if err := mcpClient.Start(ctx); err != nil {
		fail("setupSpyStdio: client.Start failed: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "spy-stdio-client", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		fail("setupSpyStdio: Initialize failed: %v", err)
	}

	t.Cleanup(func() {
		cancel()
		clientToServerWriter.Close()
		serverToClientWriter.Close()
		<-serverDone
		mcpClient.Close()
		unregisterServer(mcpServer)
	})

	return mcpClient, mock
}

// TestStdio_FullSessionPublishesOnlyToolCalls drives the whole MCP surface the
// fixture exposes over a real stdio transport and pins the v2 rule: tool calls
// are the only thing that publishes.
func TestStdio_FullSessionPublishesOnlyToolCalls(t *testing.T) {
	mcpClient, mock := setupSpyStdio(t, nil)
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

// TestStdio_SuppliedSessionRoundTrips pins that a session_id supplied by the agent
// survives a real stdio hop: every call carrying it is attributed to it, and
// none of them re-mints.
func TestStdio_SuppliedSessionRoundTrips(t *testing.T) {
	mcpClient, mock := setupSpyStdio(t, nil)

	session := sid("stdio_supplied")
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
		if evt.ClientName == nil || *evt.ClientName != "spy-stdio-client" {
			t.Errorf("event %d lost the per-request client identity: %v", i, evt.ClientName)
		}
	}
}

// TestStdio_IdentityStampsEveryCallNoIdentifyEvents pins the v2 identify
// contract over a real stdio transport: the callback runs on every tool call,
// its result stamps that call's event, and no separate identify event is ever
// published.
func TestStdio_IdentityStampsEveryCallNoIdentifyEvents(t *testing.T) {
	var identifyCount atomic.Int32
	opts := DefaultOptions()
	opts.Identify = func(ctx context.Context, request any) *agentcat.UserIdentity {
		identifyCount.Add(1)
		return &agentcat.UserIdentity{UserID: "stdio-actor", UserName: "Stdio Actor"}
	}

	mcpClient, mock := setupSpyStdio(t, opts)
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
		if evt.IdentifyActorGivenId == nil || *evt.IdentifyActorGivenId != "stdio-actor" {
			t.Errorf("event %d not stamped with the actor: %v", i, evt.IdentifyActorGivenId)
		}
	}
	if identifies := filterEvents(mock.getEvents(), "agentcat:identify"); len(identifies) != 0 {
		t.Errorf("v2 publishes no agentcat:identify events, got %d", len(identifies))
	}
}

// TestStdio_FullPipeline verifies a basic tool call works end-to-end over a
// real stdio transport.
func TestStdio_FullPipeline(t *testing.T) {
	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	}

	mcpClient := setupStdio(t, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "add_todo"
	req.Params.Arguments = map[string]any{
		"title": "Stdio e2e todo",
	}

	result, err := mcpClient.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	if result.IsError {
		t.Fatalf("CallTool returned error result: %s", resultText(result))
	}

	assertContains(t, resultText(result), "Stdio e2e todo")
}

// TestStdio_IdentifyInvoked verifies that the Identify callback fires when a
// real stdio session is present.
func TestStdio_IdentifyInvoked(t *testing.T) {
	var identifyCount atomic.Int32

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
			identifyCount.Add(1)
			return &agentcat.UserIdentity{
				UserID:   "stdio-user-1",
				UserName: "Stdio Test User",
			}
		},
	}

	mcpClient := setupStdio(t, opts)

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
		t.Error("expected Identify to be called at least once over stdio transport, but it was not")
	}
}

// TestStdio_ServerInfo verifies that the server name and version returned
// during initialization match what was configured.
func TestStdio_ServerInfo(t *testing.T) {
	mcpServer, _ := CreateFullServer()

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	}
	_, err := Track(mcpServer, "test_project", opts)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}

	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()

	stdioServer := server.NewStdioServer(mcpServer)
	stdioServer.SetErrorLogger(log.New(io.Discard, "", 0))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- stdioServer.Listen(ctx, clientToServerReader, serverToClientWriter)
	}()

	trans := transport.NewIO(serverToClientReader, clientToServerWriter, nil)

	if err := trans.Start(ctx); err != nil {
		cancel()
		clientToServerWriter.Close()
		serverToClientWriter.Close()
		unregisterServer(mcpServer)
		t.Fatalf("trans.Start failed: %v", err)
	}

	mcpClient := client.NewClient(trans)

	if err := mcpClient.Start(ctx); err != nil {
		cancel()
		clientToServerWriter.Close()
		serverToClientWriter.Close()
		unregisterServer(mcpServer)
		t.Fatalf("client.Start failed: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "info-check-stdio-client",
		Version: "2.0.0",
	}

	initResult, err := mcpClient.Initialize(ctx, initReq)
	if err != nil {
		cancel()
		clientToServerWriter.Close()
		serverToClientWriter.Close()
		unregisterServer(mcpServer)
		t.Fatalf("Initialize failed: %v", err)
	}

	t.Cleanup(func() {
		cancel()
		clientToServerWriter.Close()
		serverToClientWriter.Close()
		<-serverDone
		mcpClient.Close()
		unregisterServer(mcpServer)
	})

	if initResult.ServerInfo.Name != "todo-server" {
		t.Errorf("expected ServerInfo.Name=%q, got %q", "todo-server", initResult.ServerInfo.Name)
	}
	if initResult.ServerInfo.Version != "1.0.0" {
		t.Errorf("expected ServerInfo.Version=%q, got %q", "1.0.0", initResult.ServerInfo.Version)
	}
}
