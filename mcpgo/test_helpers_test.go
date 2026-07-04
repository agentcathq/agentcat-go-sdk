package mcpgo

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
)

// testHarness bundles a tracked MCP server, its in-process client, and the
// backing TodoStore so that integration tests can focus on assertions rather
// than boilerplate setup.
type testHarness struct {
	Server *server.MCPServer
	Client client.MCPClient
	Store  *TodoStore
	t      *testing.T
}

// newHarness creates a fully-initialised test harness:
//   - builds the full todo server (tools + resources + prompts)
//   - enables MCPCat tracking with the supplied options
//   - starts an in-process MCP client and completes the initialize handshake
//   - registers cleanup functions via t.Cleanup
func newHarness(t *testing.T, opts *Options) *testHarness {
	t.Helper()

	mcpServer, store := CreateFullServer()

	_, err := Track(mcpServer, "test_project", opts)
	if err != nil {
		t.Fatalf("newHarness: Track failed: %v", err)
	}

	mcpClient, err := client.NewInProcessClient(mcpServer)
	if err != nil {
		unregisterServer(mcpServer)
		t.Fatalf("newHarness: NewInProcessClient failed: %v", err)
	}

	ctx := context.Background()
	if err := mcpClient.Start(ctx); err != nil {
		mcpClient.Close()
		unregisterServer(mcpServer)
		t.Fatalf("newHarness: client.Start failed: %v", err)
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}

	_, err = mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		mcpClient.Close()
		unregisterServer(mcpServer)
		t.Fatalf("newHarness: Initialize failed: %v", err)
	}

	t.Cleanup(func() {
		mcpClient.Close()
		unregisterServer(mcpServer)
	})

	return &testHarness{
		Server: mcpServer,
		Client: mcpClient,
		Store:  store,
		t:      t,
	}
}

// callTool invokes the named tool with the given arguments and returns the
// result. It calls t.Fatal on any transport-level error.
func (h *testHarness) callTool(name string, args map[string]any) *mcp.CallToolResult {
	h.t.Helper()

	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := h.Client.CallTool(context.Background(), req)
	if err != nil {
		h.t.Fatalf("callTool(%q): %v", name, err)
	}
	return result
}

// resultText extracts the text from the first TextContent entry in a
// CallToolResult. It returns an empty string when no text content is found.
func resultText(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	if tc, ok := result.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// assertContains fails the test if s does not contain substr.
func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", s, substr)
	}
}

// EventSpy captures events for testing
type EventSpy struct {
	mu     sync.Mutex
	events []*agentcat.Event
}

func (s *EventSpy) Capture(event *agentcat.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *EventSpy) GetEvents() []*agentcat.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*agentcat.Event, len(s.events))
	copy(result, s.events)
	return result
}

func (s *EventSpy) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func (s *EventSpy) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = nil
}
