package mcpgo

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk/v2"
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

// newTestInstance builds the AgentCatInstance that Track would register for
// these options, so hand-wired tests carry the same tracked config.
func newTestInstance(mcpServer *server.MCPServer, projectID string, opts *Options) *agentcat.AgentCatInstance {
	return &agentcat.AgentCatInstance{
		ProjectID: projectID,
		Options: &agentcat.Options{
			DisableReportMissing:     opts.DisableReportMissing,
			DisableToolCallContext:   opts.DisableToolCallContext,
			DisableTracing:           opts.DisableTracing,
			CustomContextDescription: opts.CustomContextDescription,
			EnableAgentTracking:      opts.EnableAgentTracking,
			Debug:                    opts.Debug,
		},
	}
}

// callToolRaw drives a tools/call through the server's own message entry point
// — the path every mcp-go transport takes — and decodes the tool result.
func callToolRaw(t *testing.T, mcpServer *server.MCPServer, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatalf("marshal tools/call request: %v", err)
	}

	raw, err := json.Marshal(mcpServer.HandleMessage(context.Background(), request))
	if err != nil {
		t.Fatalf("marshal tools/call response: %v", err)
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode tools/call response %s: %v", raw, err)
	}
	if len(envelope.Result) == 0 {
		t.Fatalf("tools/call(%q) failed: %s", name, raw)
	}

	var result mcp.CallToolResult
	if err := json.Unmarshal(envelope.Result, &result); err != nil {
		t.Fatalf("decode CallToolResult %s: %v", envelope.Result, err)
	}
	return &result
}

// decodeEchoedArgs decodes the arguments the echo_args fixture reported.
func decodeEchoedArgs(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	var echoed map[string]any
	text := resultText(result)
	if err := json.Unmarshal([]byte(text), &echoed); err != nil {
		t.Fatalf("decode echoed args %q: %v", text, err)
	}
	return echoed
}

// The two settle windows a test may use to prove that NO event arrives.
// Absence has no signal to wait on, so it can only be observed by giving a
// stray event time to land. These are the only two such numbers in the package.
const (
	// settleWindow covers capture → mock publisher: a direct function call from
	// the capture site.
	settleWindow = 200 * time.Millisecond

	// settlePublishWindow covers capture → the real publisher's worker pool →
	// an HTTP POST to a local capture API. A strictly longer path than
	// settleWindow, so it gets a strictly longer window.
	settlePublishWindow = 500 * time.Millisecond
)

// settleForAbsentEvents waits out settleWindow. Use it only where the assertion
// is about the ABSENCE of an event; every other wait must key off a signal.
func settleForAbsentEvents() { time.Sleep(settleWindow) }

// settleForAbsentPublishes is settleForAbsentEvents for tests that assert
// against the real publisher's HTTP output rather than a mock publisher.
func settleForAbsentPublishes() { time.Sleep(settlePublishWindow) }

// mockPublisher collects published events for test assertions.
type mockPublisher struct {
	mu     sync.Mutex
	events []*agentcat.Event
}

func (m *mockPublisher) publish(evt *agentcat.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, evt)
}

func (m *mockPublisher) getEvents() []*agentcat.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*agentcat.Event, len(m.events))
	copy(cp, m.events)
	return cp
}

func (m *mockPublisher) waitForEvents(n int, timeout time.Duration) []*agentcat.Event {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events := m.getEvents()
		if len(events) >= n {
			return events
		}
		time.Sleep(10 * time.Millisecond)
	}
	return m.getEvents()
}

// filterEvents returns events matching the given event type.
func filterEvents(events []*agentcat.Event, eventType string) []*agentcat.Event {
	var filtered []*agentcat.Event
	for _, evt := range events {
		if evt.EventType != nil && *evt.EventType == eventType {
			filtered = append(filtered, evt)
		}
	}
	return filtered
}

// eventTypes returns a slice of event type strings for debugging.
func eventTypes(events []*agentcat.Event) []string {
	var types []string
	for _, evt := range events {
		if evt.EventType != nil {
			types = append(types, *evt.EventType)
		} else {
			types = append(types, "<nil>")
		}
	}
	return types
}

// setupSpyHTTP wires a tracked server over a real HTTP transport with events
// going to a mock publisher, so tests can assert on the exact events that
// would be sent to AgentCat (including transport metadata like headers).
func setupSpyHTTP(t *testing.T, opts *Options) (*client.Client, *mockPublisher) {
	t.Helper()
	mcpServer, _ := CreateFullServer()
	return setupSpyHTTPOn(t, mcpServer, opts)
}

// setupSpyHTTPOn is setupSpyHTTP for an already-built server. Only the SSE and
// streamable-HTTP sessions implement server.SessionWithTools, so tests that
// need session-scoped tools must use this transport.
func setupSpyHTTPOn(t *testing.T, mcpServer *server.MCPServer, opts *Options) (*client.Client, *mockPublisher) {
	t.Helper()

	if opts == nil {
		opts = DefaultOptions()
	}

	instance := newTestInstance(mcpServer, "test_project", opts)
	agentcat.RegisterServer(mcpServer, instance)

	mock := &mockPublisher{}
	installTracking(mcpServer, instance, opts, mock.publish)

	httpServer := server.NewTestStreamableHTTPServer(mcpServer)

	mcpClient, err := client.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		httpServer.Close()
		unregisterServer(mcpServer)
		t.Fatalf("setupSpyHTTP: NewStreamableHttpClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	if err := mcpClient.Start(ctx); err != nil {
		cancel()
		mcpClient.Close()
		httpServer.Close()
		unregisterServer(mcpServer)
		t.Fatalf("setupSpyHTTP: client.Start failed: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "spy-http-client",
		Version: "1.0.0",
	}

	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		cancel()
		mcpClient.Close()
		httpServer.Close()
		unregisterServer(mcpServer)
		t.Fatalf("setupSpyHTTP: Initialize failed: %v", err)
	}

	t.Cleanup(func() {
		mcpClient.Close()
		httpServer.Close()
		cancel()
		unregisterServer(mcpServer)
	})

	return mcpClient, mock
}

// spyHarness is a tracked server whose events go to a mock publisher instead
// of the real one, plus an initialised in-process client. It installs exactly
// what Track installs (installTracking), so the tests exercise real wiring.
type spyHarness struct {
	Server *server.MCPServer
	Client *client.Client
	Mock   *mockPublisher
	t      *testing.T
}

// newSpyHarness builds a spy harness for the default test project.
func newSpyHarness(t *testing.T, opts *Options) *spyHarness {
	t.Helper()
	return newSpyHarnessWithProject(t, opts, "proj_test")
}

// newSpyHarnessWithProject is newSpyHarness with an explicit project ID (the
// hook-mode golden vector is pinned to proj_1).
func newSpyHarnessWithProject(t *testing.T, opts *Options, projectID string) *spyHarness {
	t.Helper()

	mcpServer, _ := CreateFullServer()
	registerHandleFixtures(mcpServer)
	return newSpyHarnessOn(t, mcpServer, opts, projectID)
}

// newSpyHarnessOn tracks an already-built server (so tests can choose server
// options such as schema validation) and connects an in-process client.
func newSpyHarnessOn(t *testing.T, mcpServer *server.MCPServer, opts *Options, projectID string) *spyHarness {
	t.Helper()

	if opts == nil {
		opts = DefaultOptions()
	}

	instance := newTestInstance(mcpServer, projectID, opts)
	agentcat.RegisterServer(mcpServer, instance)

	mock := &mockPublisher{}
	installTracking(mcpServer, instance, opts, mock.publish)

	mcpClient, err := client.NewInProcessClient(mcpServer)
	if err != nil {
		unregisterServer(mcpServer)
		t.Fatalf("newSpyHarness: NewInProcessClient failed: %v", err)
	}

	ctx := context.Background()
	if err := mcpClient.Start(ctx); err != nil {
		mcpClient.Close()
		unregisterServer(mcpServer)
		t.Fatalf("newSpyHarness: client.Start failed: %v", err)
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "handles-test-client",
		Version: "0.1.0",
	}
	if _, err := mcpClient.Initialize(ctx, initRequest); err != nil {
		mcpClient.Close()
		unregisterServer(mcpServer)
		t.Fatalf("newSpyHarness: Initialize failed: %v", err)
	}

	t.Cleanup(func() {
		mcpClient.Close()
		unregisterServer(mcpServer)
	})

	return &spyHarness{Server: mcpServer, Client: mcpClient, Mock: mock, t: t}
}

// call invokes a tool and returns the wire result plus the single
// mcp:tools/call event that call published.
func (h *spyHarness) call(name string, args map[string]any) (*mcp.CallToolResult, *agentcat.Event) {
	h.t.Helper()
	return h.callWithMeta(name, args, nil)
}

// callWithMeta is call with reserved _meta fields on the tools/call params.
func (h *spyHarness) callWithMeta(name string, args map[string]any, meta map[string]any) (*mcp.CallToolResult, *agentcat.Event) {
	h.t.Helper()

	before := len(filterEvents(h.Mock.getEvents(), "mcp:tools/call"))

	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	if meta != nil {
		req.Params.Meta = &mcp.Meta{AdditionalFields: meta}
	}

	result, err := h.Client.CallTool(context.Background(), req)
	if err != nil {
		h.t.Fatalf("callTool(%q): %v", name, err)
	}

	events := h.waitForToolCallEvents(before + 1)
	if len(events) < before+1 {
		h.t.Fatalf("no mcp:tools/call event published for %q", name)
	}
	return result, events[before]
}

// waitForToolCallEvents waits until at least n mcp:tools/call events have been
// published (capture is synchronous, so this normally returns immediately).
func (h *spyHarness) waitForToolCallEvents(n int) []*agentcat.Event {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		events := filterEvents(h.Mock.getEvents(), "mcp:tools/call")
		if len(events) >= n || time.Now().After(deadline) {
			return events
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// listTools drives a real tools/list through the harness client.
func (h *spyHarness) listTools() *mcp.ListToolsResult {
	h.t.Helper()
	result, err := h.Client.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		h.t.Fatalf("ListTools: %v", err)
	}
	return result
}

// firstText returns the text of the FIRST content block of a result — the
// position where this SDK prepends its mint-back block.
func firstText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil || len(result.Content) == 0 {
		t.Fatal("result has no content")
	}
	first := result.Content[0]
	tc, ok := first.(mcp.TextContent)
	if !ok {
		t.Fatalf("first content block is %T, want mcp.TextContent", first)
	}
	return tc.Text
}

// listToolRawJSON returns the named tool's inputSchema exactly as it goes on
// the wire. It drives the server's own request handler with a raw tools/list
// message — the same entry point every mcp-go transport uses (the in-process
// transport marshals the request, calls HandleMessage, and marshals the
// response) — because the typed client decodes inputSchema into a Go map and
// loses property order.
func listToolRawJSON(t *testing.T, mcpServer *server.MCPServer, toolName string) string {
	t.Helper()

	response := mcpServer.HandleMessage(
		context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`),
	)
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal tools/list response: %v", err)
	}

	var envelope struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode tools/list response %s: %v", raw, err)
	}
	for _, tool := range envelope.Result.Tools {
		if tool.Name == toolName {
			return string(tool.InputSchema)
		}
	}
	t.Fatalf("tool %q not found in tools/list response: %s", toolName, raw)
	return ""
}

// resultText extracts the text of the first TextContent entry in a
// CallToolResult that is not this SDK's own leading mint-back block, so
// assertions about the CUSTOMER's text hold whether or not the call was
// decorated. It returns an empty string when no such content is found.
func resultText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			if isMintBackText(tc.Text) {
				continue
			}
			return tc.Text
		}
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

// sid returns a syntactically valid session ID that still reads as its label
// in a failure message. Real KSUIDs are opaque; test fixtures should not be.
//
// The body is exactly 27 ASCII base62 characters, so it satisfies
// agentcat.IsValidSessionID by construction and the slice can never split a
// rune. Use it for any ses_ literal that travels as a session_id tool-call
// argument, or that must equal one — a short literal like sid("abc") is
// rejected as a value this server never issued. Literals that seed an Event,
// an exporter, or the redactor directly are never resolved and need no helper.
func sid(label string) string {
	var b strings.Builder
	for _, r := range label {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	return "ses_" + (b.String() + strings.Repeat("0", 27))[:27]
}

// mintBackFor renders the text block this SDK prepends on the call that
// minted sessionID. Tests assert against the real generator rather than a
// copied literal, so a copy change cannot pass here and fail on the wire.
func mintBackFor(sessionID string) string {
	return agentcat.BuildMintBackText(agentcat.SessionResolution{
		SessionID: sessionID,
		Source:    agentcat.SessionSourceMinted,
	})
}
