package officialsdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// ── per-request factory topology ─────────────────────────────────────────────

// factoryServers records every server a per-request getServer factory builds,
// so a test can inspect the instances the requests actually landed on.
type factoryServers struct {
	mu   sync.Mutex
	list []*mcp.Server
}

func (f *factoryServers) add(s *mcp.Server) {
	f.mu.Lock()
	f.list = append(f.list, s)
	f.mu.Unlock()
}

func (f *factoryServers) all() []*mcp.Server {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*mcp.Server(nil), f.list...)
}

// trackFreshTodoServer builds the todo fixture and installs exactly the wiring
// Track installs, with events routed to mock. It registers no t.Cleanup: the
// factory calls it from the HTTP server's goroutines, and the caller owns
// unregistering every server it handed out.
func trackFreshTodoServer(t *testing.T, mock *mockPublisher, projectID string, opts *Options) *mcp.Server {
	t.Helper()

	server, _ := createFullTestServer(t)
	if opts == nil {
		opts = DefaultOptions()
	}
	coreOpts := &agentcat.Options{
		DisableReportMissing:     opts.DisableReportMissing,
		DisableToolCallContext:   opts.DisableToolCallContext,
		DisableTracing:           opts.DisableTracing,
		CustomContextDescription: opts.CustomContextDescription,
		EnableAgentTracking:      opts.EnableAgentTracking,
	}
	agentcat.RegisterServer(server, &agentcat.AgentCatInstance{ProjectID: projectID, Options: coreOpts})
	middleware := newTrackingMiddleware(server, projectID, opts, mock.publish,
		&mcp.Implementation{Name: "todo-test-server", Version: "1.0.0"})
	server.AddReceivingMiddleware(middleware)
	registerGetMoreToolsIfEnabled(server, coreOpts)
	return server
}

// newFactoryServer exposes a getServer factory over a real streamable HTTP
// endpoint in Stateless mode: a fresh mcp.Server is built and tracked on every
// single HTTP request, which is the topology the v2 registries and the
// registry's weak lifecycle both exist for.
func newFactoryServer(t *testing.T, opts *Options) (*httptest.Server, *mockPublisher, *factoryServers) {
	t.Helper()

	mock := &mockPublisher{}
	built := &factoryServers{}

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		s := trackFreshTodoServer(t, mock, "proj_1", opts)
		built.add(s)
		return s
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	ts := httptest.NewServer(handler)
	t.Cleanup(func() {
		ts.Close()
		for _, s := range built.all() {
			agentcat.UnregisterServer(s)
		}
	})
	return ts, mock, built
}

// connectHTTPClient connects a real go-sdk client over the streamable HTTP
// transport at url.
func connectHTTPClient(t *testing.T, url string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "factory-e2e-client", Version: "1.0.0"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// rebuiltInstances counts the factory-built servers whose registries were
// derived on the call path — none of them ever served a tools/list.
func rebuiltInstances(built *factoryServers) int {
	n := 0
	for _, s := range built.all() {
		if inst := getMCPcat(s); inst != nil && inst.Registries.Load() != nil {
			n++
		}
	}
	return n
}

// TestFactoryTopologyRebuildOnDemand drives tools/call FIRST against a
// Stateless factory server: the instance serving the call has never seen a
// tools/list, so the SDK must rebuild its registries on the call path and still
// strip, mint back, and record the raw request.
func TestFactoryTopologyRebuildOnDemand(t *testing.T) {
	ts, mock, built := newFactoryServer(t, nil)
	cs := connectHTTPClient(t, ts.URL)

	// No ListTools: this is the first tools/call this process ever makes.
	res, evt := callToolOn(t, cs, mock, "echo_args", map[string]any{
		"payload": "keep",
		"context": "why",
	})

	// (a) The handler received the stripped arguments.
	echoed := decodeEchoedArgs(t, res)
	if _, has := echoed["context"]; has {
		t.Errorf("rebuild-on-demand strip failed: handler saw context (%v)", echoed)
	}
	if _, has := echoed["session_id"]; has {
		t.Errorf("rebuild-on-demand strip failed: handler saw session_id (%v)", echoed)
	}
	if echoed["payload"] != "keep" {
		t.Errorf("real argument lost in stripping: %v", echoed)
	}

	// (b) The wire result carries the mint-back for the session this call minted.
	minted := evt.GetSessionId()
	if !strings.HasPrefix(minted, "ses_") {
		t.Fatalf("expected a minted ses_ session, got %q", minted)
	}
	if got := firstText(t, res); got != mintBackFor(minted) {
		t.Errorf("mint-back missing or wrong on the rebuilt instance:\n got:  %q\n want: %q",
			got, mintBackFor(minted))
	}

	// (c) The event records the RAW request, context included.
	args, ok := evt.Parameters["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("expected recorded arguments map, got %v", evt.Parameters)
	}
	if args["context"] != "why" || args["payload"] != "keep" {
		t.Errorf("raw event args must include context verbatim, got %v", args)
	}
	if evt.UserIntent == nil || *evt.UserIntent != "why" {
		t.Errorf("user intent = %v, want %q", evt.UserIntent, "why")
	}

	// (d) The rebuild really happened on the call path.
	if n := rebuiltInstances(built); n == 0 {
		t.Error("no factory instance rebuilt its registries on the call path")
	}
}

// TestFactoryTopologySuppliedSessionRoundTrips pins that a supplied session_id
// survives the real HTTP hop into a brand-new per-request instance: the event
// is attributed to it and no fresh session is minted back.
func TestFactoryTopologySuppliedSessionRoundTrips(t *testing.T) {
	ts, mock, _ := newFactoryServer(t, nil)
	cs := connectHTTPClient(t, ts.URL)

	res, evt := callToolOn(t, cs, mock, "echo_args", map[string]any{
		"payload":    "keep",
		"session_id": sid("factory_supplied"),
	})

	if evt.GetSessionId() != sid("factory_supplied") {
		t.Errorf("supplied session must attribute the event, got %q", evt.GetSessionId())
	}
	if evt.Tags == nil || (*evt.Tags)[agentcat.TagSessionSource] != string(agentcat.SessionSourceSupplied) {
		t.Errorf("session source tag = %v, want %q", evt.Tags, agentcat.SessionSourceSupplied)
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "session_id issued") {
			t.Error("a supplied session must never be re-minted back")
		}
	}
}

// TestFactoryTopologyRebuildGatesTheMirror is the discriminating assertion for
// the rebuild: the mirror gate FALLS OPEN when there are no registries
// (ShouldMirror returns true for a nil registry), so a tool with no declared
// output schema getting no mirror can only be explained by registries that were
// actually rebuilt from the instance's own tool list.
func TestFactoryTopologyRebuildGatesTheMirror(t *testing.T) {
	ts, mock, _ := newFactoryServer(t, nil)
	cs := connectHTTPClient(t, ts.URL)

	res, _ := callToolOn(t, cs, mock, "structured_only", map[string]any{})

	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %T, want map", res.StructuredContent)
	}
	if _, has := sc[agentcat.MCPSessionKey]; has {
		t.Error("structured_only declares no output schema: the rebuilt registry must keep the mirror off")
	}
	if sc["ok"] != true {
		t.Errorf("customer structured content lost: %v", sc)
	}
}

// TestFactoryTopologyPublishesOneEventPerCall pins that repeated calls against
// a topology that rebuilds a server per request still publish exactly one
// mcp:tools/call event each — and nothing else.
func TestFactoryTopologyPublishesOneEventPerCall(t *testing.T) {
	ts, mock, built := newFactoryServer(t, nil)
	cs := connectHTTPClient(t, ts.URL)
	ctx := context.Background()

	const calls = 4
	for i := range calls {
		if _, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name:      "echo_args",
			Arguments: map[string]any{"payload": "p", "session_id": sid("factory_loop")},
		}); err != nil {
			t.Fatalf("CallTool %d: %v", i, err)
		}
	}

	waitForEventType(mock, "mcp:tools/call", calls, 3*time.Second)
	settleForAbsentEvents()

	events := mock.getEvents()
	if got := len(events); got != calls {
		t.Errorf("expected exactly %d events, got %d (%v)", calls, got, eventTypes(events))
	}
	for _, e := range events {
		if e.EventType == nil || *e.EventType != "mcp:tools/call" {
			t.Errorf("unexpected event type %v — v2 publishes only tool calls", e.EventType)
		}
		if e.GetSessionId() != sid("factory_loop") {
			t.Errorf("event attributed to %q, want ses_factory_loop", e.GetSessionId())
		}
	}
	// Stateless mode builds a server per HTTP request, so the factory ran at
	// least once per call — the topology under test really is per-request.
	if n := len(built.all()); n < calls {
		t.Errorf("factory built %d servers for %d calls: this is not the per-request topology", n, calls)
	}
}

// decodeEchoedArgs decodes the arguments the echo_args fixture reported.
func decodeEchoedArgs(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("echo_args returned no content")
	}
	// The customer's own text is the LAST block (a mint-back, when present,
	// is prepended as the first).
	tc, ok := res.Content[len(res.Content)-1].(*mcp.TextContent)
	if !ok {
		t.Fatalf("echo content = %T, want *mcp.TextContent", res.Content[len(res.Content)-1])
	}
	var echoed map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &echoed); err != nil {
		t.Fatalf("decode echoed args %q: %v", tc.Text, err)
	}
	return echoed
}
