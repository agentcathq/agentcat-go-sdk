package officialsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// ── fixtures ─────────────────────────────────────────────────────────────────

// connectClient connects a client session over in-memory transports using the
// go-sdk default (2026-07-28) protocol: client identity travels in per-request
// _meta and no initialize request reaches the middleware.
func connectClient(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "handles-test-client", Version: "0.1.0"}, nil)
	clientSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		clientSession.Close()
		serverSession.Wait()
	})
	return clientSession
}

// callToolOn calls a tool through the client session and waits for the single
// mcp:tools/call event that call publishes.
func callToolOn(t *testing.T, cs *mcp.ClientSession, mock *mockPublisher, name string, args map[string]any) (*mcp.CallToolResult, *agentcat.Event) {
	t.Helper()
	before := len(filterEvents(mock.getEvents(), "mcp:tools/call"))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	events := waitForEventType(mock, "mcp:tools/call", before+1, 3*time.Second)
	if len(events) < before+1 {
		t.Fatalf("no mcp:tools/call event published for %s", name)
	}
	return res, events[before]
}

// lastText returns the text of the final content block of a result.
func lastText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := res.Content[len(res.Content)-1].(*mcp.TextContent)
	if !ok {
		t.Fatalf("last content block is %T, want *mcp.TextContent", res.Content[len(res.Content)-1])
	}
	return tc.Text
}

// buildDirectCallHandler composes the tracking middleware around a fake next
// handler, so tests can drive tools/call directly with synthetic requests and
// observe exactly what is dispatched. Returns the composed handler and the
// registered instance.
func buildDirectCallHandler(t *testing.T, opts *Options, mock *mockPublisher, next mcp.MethodHandler) (mcp.MethodHandler, *agentcat.AgentCatInstance) {
	t.Helper()
	if opts == nil {
		opts = DefaultOptions()
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "direct-server", Version: "0.0.1"}, nil)
	coreOpts := &agentcat.Options{
		DisableReportMissing:     opts.DisableReportMissing,
		DisableToolCallContext:   opts.DisableToolCallContext,
		DisableTracing:           opts.DisableTracing,
		CustomContextDescription: opts.CustomContextDescription,
		EnableAgentTracking:      opts.EnableAgentTracking,
	}
	instance := &agentcat.AgentCatInstance{
		ProjectID: "proj_direct",
		Options:   coreOpts,
	}
	agentcat.RegisterServer(server, instance)
	t.Cleanup(func() { agentcat.UnregisterServer(server) })

	mw := newTrackingMiddleware(server, "proj_direct", opts, mock.publish, nil)
	return mw(next), instance
}

// ── mint + mint-back ─────────────────────────────────────────────────────────

func TestCallMintsSessionAndMintsBack(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, nil)
	cs := connectClient(t, server)

	// First call WITHOUT session_id.
	res, evt := callToolOn(t, cs, mock, "add_todo", map[string]any{
		"title":   "hi",
		"context": "adding my first todo",
	})

	// 1. The wire response carries the trailing mint-back text block…
	if got := lastText(t, res); !strings.HasPrefix(got, "[MCP INSTRUCTIONS]: session_id issued.") {
		t.Errorf("missing mint-back block, last content = %q", got)
	}
	// …and the published event does NOT (wire-only discipline).
	resp := fmt.Sprint(evt.Response)
	if strings.Contains(resp, "MCP INSTRUCTIONS") {
		t.Errorf("mint-back leaked into the published event response: %s", resp)
	}
	if strings.Contains(resp, "_mcp_instructions") {
		t.Errorf("mirror leaked into the published event response: %s", resp)
	}

	// 2. Event session is the minted ses_ id; tag records the source; the
	// mint-back names the exact session the event was attributed to.
	if !strings.HasPrefix(evt.GetSessionId(), "ses_") {
		t.Errorf("sessionId = %q, want ses_ prefix", evt.GetSessionId())
	}
	if evt.Tags == nil || (*evt.Tags)["agentcat_session_id_source"] != "minted" {
		t.Errorf("session source tag missing or wrong: %v", evt.Tags)
	}
	if !strings.Contains(lastText(t, res), evt.GetSessionId()) {
		t.Error("mint-back text does not name the event's session id")
	}

	// 3. The event records the RAW request (context included) and user intent.
	args, _ := evt.Parameters["arguments"].(map[string]any)
	if args["title"] != "hi" {
		t.Errorf("raw request must be recorded, got parameters %v", evt.Parameters)
	}
	if args["context"] != "adding my first todo" {
		t.Errorf("raw request must include context verbatim, got %v", args)
	}
	if evt.UserIntent == nil || *evt.UserIntent != "adding my first todo" {
		t.Errorf("user intent = %v, want the context value", evt.UserIntent)
	}

	// 4. A second mint call starts a NEW session: nothing is remembered.
	_, evt2 := callToolOn(t, cs, mock, "add_todo", map[string]any{"title": "again", "context": "x"})
	if evt2.GetSessionId() == evt.GetSessionId() {
		t.Error("two mint calls must mint distinct sessions (stateless per call)")
	}
}

func TestCallWithSuppliedSessionEchoesWithoutReminting(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, nil)
	cs := connectClient(t, server)

	res, evt := callToolOn(t, cs, mock, "add_todo", map[string]any{"title": "hi", "session_id": sid("supplied")})
	if evt.GetSessionId() != sid("supplied") {
		t.Errorf("supplied session must be trusted verbatim, got %q", evt.GetSessionId())
	}
	if evt.Tags == nil || (*evt.Tags)["agentcat_session_id_source"] != "supplied" {
		t.Errorf("tag must be supplied, got %v", evt.Tags)
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "session_id issued") {
			t.Error("no text mint-back on supplied calls")
		}
	}
	// The raw record keeps the supplied handle.
	args, _ := evt.Parameters["arguments"].(map[string]any)
	if args["session_id"] != sid("supplied") {
		t.Errorf("raw arguments must include the supplied session_id, got %v", args)
	}
}

func TestErrorResultsStillGetMintBack(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, nil)
	cs := connectClient(t, server)

	res, evt := callToolOn(t, cs, mock, "always_fails", map[string]any{})
	if !res.IsError {
		t.Fatal("fixture tool must return an IsError result")
	}
	if got := lastText(t, res); !strings.Contains(got, "session_id issued") {
		t.Errorf("mint-back must be appended to isError results too, got %q", got)
	}
	if evt.IsError == nil || !*evt.IsError {
		t.Error("event must record the tool error")
	}
}

// TestMintBackReachesToolsThatReturnNoContent pins the mint-back on tools that
// return structuredContent only. The go-sdk hands the middleware an EMPTY
// content slice for those, and copying an empty slice yields nil — if the
// mint-back skipped nil content, such agents would never learn their session and
// every call would mint a fresh one, fragmenting the analytics session.
func TestMintBackReachesToolsThatReturnNoContent(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, nil)
	cs := connectClient(t, server)

	res, evt := callToolOn(t, cs, mock, "structured_only", map[string]any{})

	if got := lastText(t, res); !strings.Contains(got, "session_id issued") {
		t.Errorf("mint-back must be appended to content-less results too, got %q", got)
	}
	if !strings.Contains(lastText(t, res), evt.GetSessionId()) {
		t.Error("mint-back text does not name the event's session id")
	}
	// Wire-only: the published event carries the customer's original result.
	resp := fmt.Sprint(evt.Response)
	if strings.Contains(resp, "MCP INSTRUCTIONS") || strings.Contains(resp, "_mcp_instructions") {
		t.Errorf("decoration leaked into the published event response: %s", resp)
	}
}

// ── structured mirror ────────────────────────────────────────────────────────

func TestStructuredMirror(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, nil)
	cs := connectClient(t, server)

	// List first so the output-injection registry exists.
	if _, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	// add_todo declares a plain-object output schema (typed AddTool), so the
	// mirror re-confirms supplied handles on every response.
	res, evt := callToolOn(t, cs, mock, "add_todo", map[string]any{"title": "x", "session_id": sid("s")})
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %T, want a JSON object", res.StructuredContent)
	}
	mi, ok := sc["_mcp_instructions"].(map[string]any)
	if !ok {
		t.Fatalf("mirror missing from structuredContent: %v", sc)
	}
	if mi["session_id"] != sid("s") {
		t.Errorf("mirror must re-confirm supplied handles on every response, got %v", mi["session_id"])
	}
	if instr, _ := mi["instructions"].(string); !strings.Contains(instr, "confirmed") {
		t.Errorf("supplied handles get the confirmed copy, got %q", instr)
	}
	// The customer's own structured payload survives alongside the mirror.
	if sc["text"] == nil {
		t.Errorf("customer structured payload lost: %v", sc)
	}
	// Event purity: the published response carries no mirror.
	if strings.Contains(fmt.Sprint(evt.Response), "_mcp_instructions") {
		t.Error("mirror leaked into the published event")
	}

	// A minted call mirrors the new session with the full issued-session copy.
	res2, evt2 := callToolOn(t, cs, mock, "add_todo", map[string]any{"title": "y"})
	sc2, _ := res2.StructuredContent.(map[string]any)
	mi2, ok := sc2["_mcp_instructions"].(map[string]any)
	if !ok {
		t.Fatalf("mirror missing on minted call: %v", sc2)
	}
	if mi2["session_id"] != evt2.GetSessionId() {
		t.Errorf("mirror session %v != event session %q", mi2["session_id"], evt2.GetSessionId())
	}
	if instr, _ := mi2["instructions"].(string); !strings.Contains(instr, "session_id issued") {
		t.Errorf("minted mirror must carry the issued-session instructions, got %q", instr)
	}
}

// TestStructuredMirrorPreservesNumbersOnTheWire pins number fidelity through
// the mirror. Typed tools store structuredContent as json.RawMessage, so every
// mirrored response is re-encoded from the decoded value: decoding numbers as
// float64 would silently change the customer's payload (and break an
// "type":"integer" output schema) on the dominant call path.
func TestStructuredMirrorPreservesNumbersOnTheWire(t *testing.T) {
	mock := &mockPublisher{}
	raw := json.RawMessage(`{"exact":9007199254740993,"id":1234567890123456789,"nested":{"huge":12345678901234567890},"ratio":1.5}`)
	handler, _ := buildDirectCallHandler(t, nil, mock, func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method == "tools/list" {
			// A declared plain-object output schema is what gates the mirror.
			return &mcp.ListToolsResult{Tools: []*mcp.Tool{{
				Name:         "typed_tool",
				InputSchema:  map[string]any{"type": "object"},
				OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}}},
			}}}, nil
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "ok"}},
			StructuredContent: raw,
		}, nil
	})

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name:      "typed_tool",
		Arguments: json.RawMessage(`{"session_id":"` + sid("big") + `"}`),
	}}
	res, err := handler(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	wire, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal wire result: %v", err)
	}
	if !strings.Contains(string(wire), "_mcp_instructions") {
		t.Fatalf("mirror did not fire, so number fidelity is untested: %s", wire)
	}
	for _, want := range []string{
		`"exact":9007199254740993`,
		`"id":1234567890123456789`,
		`"huge":12345678901234567890`,
		`"ratio":1.5`,
	} {
		if !strings.Contains(string(wire), want) {
			t.Errorf("mirror corrupted structuredContent: want %s in %s", want, wire)
		}
	}
}

func TestNoMirrorForToolsWithoutDeclaredOutputSchema(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, nil)
	cs := connectClient(t, server)

	if _, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	// echo_args declares no output schema: mint-back stays content-only.
	res, _ := callToolOn(t, cs, mock, "echo_args", map[string]any{"payload": "p", "context": "c"})
	if res.StructuredContent != nil {
		if sc, ok := res.StructuredContent.(map[string]any); ok {
			if _, has := sc["_mcp_instructions"]; has {
				t.Error("mirror must be gated on the output-injection registry")
			}
		}
	}
}

// ── hook mode ────────────────────────────────────────────────────────────────

func TestHookMode(t *testing.T) {
	opts := &Options{
		ResolveSessionID: func(ctx context.Context, req mcp.Request) (string, error) {
			return "customer-abc", nil
		},
	}
	server, _, mock := createTodoServerWithProject(t, opts, "proj_1")
	cs := connectClient(t, server)

	res, evt := callToolOn(t, cs, mock, "add_todo", map[string]any{"title": "x"})
	// Golden vector: DeriveSessionID("customer-abc", "proj_1"), pinned cross-SDK.
	if evt.GetSessionId() != "ses_2cOHEO0LYGADMzRvWTXXVbbgxgm" {
		t.Errorf("hook derivation drifted: %q", evt.GetSessionId())
	}
	if evt.Tags == nil || (*evt.Tags)["agentcat_session_id_source"] != "hook" {
		t.Errorf("tag must be hook, got %v", evt.Tags)
	}
	// No session instructions ever: neither text block nor structured mirror.
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "MCP INSTRUCTIONS") {
			t.Errorf("hook mode shows no session instructions ever, got %q", tc.Text)
		}
	}
	if sc, ok := res.StructuredContent.(map[string]any); ok {
		if _, has := sc["_mcp_instructions"]; has {
			t.Error("hook mode must not mirror handles")
		}
	}

	// And the listed schemas have no session_id param anywhere.
	lr, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range lr.Tools {
		props, _ := marshalToMap(t, tool.InputSchema)["properties"].(map[string]any)
		if _, has := props["session_id"]; has {
			t.Errorf("hook mode injects no session_id (tool %s)", tool.Name)
		}
	}
}

func TestHookModeErrorMintsSilently(t *testing.T) {
	opts := &Options{
		ResolveSessionID: func(ctx context.Context, req mcp.Request) (string, error) {
			return "", fmt.Errorf("upstream unavailable")
		},
	}
	server, _, mock := createTodoServerWithProject(t, opts, "proj_1")
	cs := connectClient(t, server)

	res, evt := callToolOn(t, cs, mock, "add_todo", map[string]any{"title": "x"})
	if res.IsError {
		t.Error("a failing hook must never fail the customer's call")
	}
	if !strings.HasPrefix(evt.GetSessionId(), "ses_") {
		t.Errorf("hook failure must mint a session, got %q", evt.GetSessionId())
	}
	if evt.Tags == nil || (*evt.Tags)["agentcat_session_id_source"] != "minted" {
		t.Errorf("silent mint must be tagged minted, got %v", evt.Tags)
	}
	// Silent means silent: no instructions reach the agent even for the mint.
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "MCP INSTRUCTIONS") {
			t.Errorf("hook mode shows no session instructions even on silent mint, got %q", tc.Text)
		}
	}
}

// ── identity ─────────────────────────────────────────────────────────────────

// TestIdentifyEmptyUserIDDoesNotStampActor pins that a non-nil identity with an
// empty UserID leaves the actor unset rather than stamping an empty actor.
func TestIdentifyEmptyUserIDDoesNotStampActor(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, &Options{
		Identify: func(ctx context.Context, req mcp.Request) *agentcat.UserIdentity {
			return &agentcat.UserIdentity{UserName: "anonymous"}
		},
	})
	cs := connectClient(t, server)

	_, evt := callToolOn(t, cs, mock, "add_todo", map[string]any{"title": "x"})

	if evt.IdentifyActorGivenId != nil {
		t.Errorf("empty UserID must not stamp an actor, got %q", *evt.IdentifyActorGivenId)
	}
	if evt.IdentifyActorName != nil {
		t.Errorf("identity without a user id must not stamp a name, got %q", *evt.IdentifyActorName)
	}
}

// ── agent tracking ───────────────────────────────────────────────────────────

func TestAgentTracking(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, &Options{EnableAgentTracking: true})
	cs := connectClient(t, server)

	_, evt := callToolOn(t, cs, mock, "add_todo", map[string]any{
		"title": "x", "agent_id": "opus|cc|k3n9x", "session_id": sid("t"),
	})
	tags := *evt.Tags
	if tags["agentcat_agent_id"] != "opus|cc|k3n9x" || tags["agentcat_agent_id_source"] != "supplied" {
		t.Errorf("agent tags wrong: %v", tags)
	}

	// Omission never rejects the call and adds no agent tags.
	res, evt2 := callToolOn(t, cs, mock, "add_todo", map[string]any{"title": "x", "session_id": sid("t")})
	if res.IsError {
		t.Error("omitted agent_id must never reject the call")
	}
	if _, ok := (*evt2.Tags)["agentcat_agent_id"]; ok {
		t.Error("no agent id tag when agent_id omitted")
	}
	if _, ok := (*evt2.Tags)["agentcat_agent_id_source"]; ok {
		t.Error("no agent source tag when agent_id omitted")
	}
}

// ── only tool-call events ────────────────────────────────────────────────────

func TestOnlyToolCallEventsPublished(t *testing.T) {
	server, store, mock := createFullTestServerWithTracking(t, nil)

	// Legacy handshake pins initialize through the middleware; the todo
	// fixture serves resources and prompts — the richest non-tool surface.
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "v2-events-client", Version: "0.1.0"}, nil)
	pinLegacyInitializeHandshake(client)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		cs.Close()
		serverSession.Wait()
	})

	store.add("seed")
	if _, err := cs.ListTools(ctx, &mcp.ListToolsParams{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "todo://list"}); err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if _, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "summarize_todos", Arguments: map[string]string{"style": "brief"}}); err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_todos", Arguments: map[string]any{}}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	settleForAbsentEvents()

	events := mock.getEvents()
	for _, e := range events {
		if e.EventType == nil || *e.EventType != "mcp:tools/call" {
			t.Errorf("unexpected event type %v — v2 publishes only tool calls", e.EventType)
		}
	}
	if got := len(filterEvents(events, "mcp:tools/call")); got != 1 {
		t.Errorf("expected exactly 1 tool-call event, got %d (of %d total)", got, len(events))
	}
}

// ── stripping ────────────────────────────────────────────────────────────────

func TestHandlerReceivesStrippedArgsEndToEnd(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, nil)
	cs := connectClient(t, server)

	// List first so stripping is registry-driven, not heuristic.
	if _, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	res, evt := callToolOn(t, cs, mock, "echo_args", map[string]any{
		"payload": "keep", "session_id": sid("e"), "context": "why I called",
	})

	var echoed map[string]any
	first, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("echo content = %T, want text", res.Content[0])
	}
	if err := json.Unmarshal([]byte(first.Text), &echoed); err != nil {
		t.Fatalf("echoed args: %v (%q)", err, first.Text)
	}
	if _, has := echoed["session_id"]; has {
		t.Error("handler must not see the injected session_id")
	}
	if _, has := echoed["context"]; has {
		t.Error("handler must not see the injected context")
	}
	if echoed["payload"] != "keep" {
		t.Errorf("real argument lost in stripping: %v", echoed)
	}

	// The event records the raw, unstripped request.
	args, _ := evt.Parameters["arguments"].(map[string]any)
	if args["session_id"] != sid("e") || args["context"] != "why I called" || args["payload"] != "keep" {
		t.Errorf("event must record raw args, got %v", args)
	}
}

func TestGetMoreToolsCallStripsHandlesKeepsContext(t *testing.T) {
	mock := &mockPublisher{}
	var dispatched *mcp.CallToolRequest
	next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "tools/list":
			// The rebuild-on-demand path fetches the ORIGINAL list: serve
			// get_more_tools with its real (context-owning) schema.
			return &mcp.ListToolsResult{Tools: []*mcp.Tool{{
				Name: "get_more_tools",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"context": map[string]any{"type": "string"},
					},
					"required": []string{"context"},
				},
			}}}, nil
		case "tools/call":
			dispatched, _ = req.(*mcp.CallToolRequest)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "noted"}}}, nil
		}
		return nil, nil
	}
	handler, instance := buildDirectCallHandler(t, nil, mock, next)

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name:      "get_more_tools",
		Arguments: json.RawMessage(`{"session_id":"` + sid("gmt") + `","context":"need an image tool"}`),
	}}
	if _, err := handler(context.Background(), "tools/call", req); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if dispatched == nil {
		t.Fatal("tools/call never reached the inner handler")
	}

	var got map[string]any
	if err := json.Unmarshal(dispatched.Params.Arguments, &got); err != nil {
		t.Fatalf("dispatched arguments: %v", err)
	}
	if _, has := got["session_id"]; has {
		t.Error("session_id must be stripped before dispatch")
	}
	if got["context"] != "need an image tool" {
		t.Errorf("get_more_tools context must survive to the handler, got %v", got)
	}
	// The customer's request object was never mutated (strip works on a clone).
	if !strings.Contains(string(req.Params.Arguments), sid("gmt")) {
		t.Error("original request arguments must stay raw")
	}
	// The call landed before any tools/list: registries were rebuilt on demand.
	if instance.Registries.Load() == nil {
		t.Error("registries must be rebuilt on demand on the call path")
	}

	evts := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(evts) == 0 {
		t.Fatal("no tool-call event published")
	}
	if evts[0].GetSessionId() != sid("gmt") {
		t.Errorf("supplied session must attribute the event, got %q", evts[0].GetSessionId())
	}
}

// ── DisableTracing on the call path ──────────────────────────────────────────

func TestDisableTracingNoMintBackNoEvents(t *testing.T) {
	server, _, mock := createFullTestServerWithTracking(t, &Options{DisableTracing: true})
	cs := connectClient(t, server)

	// context is still injected (its own flag allows it), so it must still be
	// stripped for the call to pass the typed tool's schema validation.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "add_todo",
		Arguments: map[string]any{"title": "quiet", "context": "why"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatal("call must succeed with tracing disabled")
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "MCP INSTRUCTIONS") {
			t.Error("no mint-back when tracing is disabled")
		}
	}
	if sc, ok := res.StructuredContent.(map[string]any); ok {
		if _, has := sc["_mcp_instructions"]; has {
			t.Error("no mirror when tracing is disabled")
		}
	}

	settleForAbsentEvents()
	if events := mock.getEvents(); len(events) != 0 {
		t.Errorf("expected 0 events with DisableTracing, got %d", len(events))
	}
}

// ── integer precision through the strip path ─────────────────────────────────

func TestStrippedDispatchPreservesIntegerPrecision(t *testing.T) {
	mock := &mockPublisher{}
	var dispatched *mcp.CallToolRequest
	next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "tools/list":
			return &mcp.ListToolsResult{Tools: []*mcp.Tool{{
				Name: "big_numbers",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"exact": map[string]any{"type": "integer"},
						"huge":  map[string]any{"type": "integer"},
					},
				},
			}}}, nil
		case "tools/call":
			dispatched, _ = req.(*mcp.CallToolRequest)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		}
		return nil, nil
	}
	handler, _ := buildDirectCallHandler(t, nil, mock, next)

	// 2^53+1 silently loses its last digit through a float64 round trip;
	// 2^63 < huge < 2^64 turns into an unmarshal error for typed handlers.
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name:      "big_numbers",
		Arguments: json.RawMessage(`{"session_id":"` + sid("big") + `","context":"crunching","exact":9007199254740993,"huge":12345678901234567890}`),
	}}
	if _, err := handler(context.Background(), "tools/call", req); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if dispatched == nil {
		t.Fatal("tools/call never reached the inner handler")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(dispatched.Params.Arguments, &fields); err != nil {
		t.Fatalf("dispatched arguments: %v", err)
	}
	if _, has := fields["session_id"]; has {
		t.Error("session_id must be stripped before dispatch")
	}
	if _, has := fields["context"]; has {
		t.Error("context must be stripped before dispatch")
	}
	if got := string(fields["exact"]); got != "9007199254740993" {
		t.Errorf("exact dispatched as %s, want 9007199254740993", got)
	}
	if got := string(fields["huge"]); got != "12345678901234567890" {
		t.Errorf("huge dispatched as %s, want 12345678901234567890", got)
	}
	var typed struct {
		Exact int64  `json:"exact"`
		Huge  uint64 `json:"huge"`
	}
	if err := json.Unmarshal(dispatched.Params.Arguments, &typed); err != nil {
		t.Fatalf("typed decode of dispatched arguments: %v", err)
	}
	if typed.Exact != 9007199254740993 || typed.Huge != 12345678901234567890 {
		t.Errorf("typed values = %d/%d, want 9007199254740993/12345678901234567890", typed.Exact, typed.Huge)
	}

	// The published event's raw arguments keep the same precision.
	evts := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(evts) == 0 {
		t.Fatal("no tool-call event published")
	}
	args, _ := evts[0].Parameters["arguments"].(map[string]any)
	if n, ok := args["exact"].(json.Number); !ok || n.String() != "9007199254740993" {
		t.Errorf("event exact = %v (%T), want json.Number 9007199254740993", args["exact"], args["exact"])
	}
	if n, ok := args["huge"].(json.Number); !ok || n.String() != "12345678901234567890" {
		t.Errorf("event huge = %v (%T), want json.Number 12345678901234567890", args["huge"], args["huge"])
	}
}

// TestHandlerRawArgumentsKeepIntegerPrecisionEndToEnd pins the raw bytes the
// customer's handler receives after stripping. That is the surface the SDK
// rebuilds — and the one a handler binding from raw arguments depends on. (The
// go-sdk's own typed AddTool binding corrupts big integers through a float64
// map with or without AgentCat; that upstream behavior is not pinned here.)
func TestHandlerRawArgumentsKeepIntegerPrecisionEndToEnd(t *testing.T) {
	type bigArgs struct {
		Exact int64  `json:"exact"`
		Huge  uint64 `json:"huge"`
	}
	var received bigArgs
	var receivedRaw []byte
	server := mcp.NewServer(&mcp.Implementation{Name: "precision-server", Version: "0.0.1"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "big_numbers",
		Description: "Records the integers it receives",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"exact": map[string]any{"type": "integer"},
				"huge":  map[string]any{"type": "integer"},
			},
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		receivedRaw = append([]byte(nil), req.Params.Arguments...)
		if err := json.Unmarshal(req.Params.Arguments, &received); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	mock := &mockPublisher{}
	instance := &agentcat.AgentCatInstance{ProjectID: "proj_precision", Options: &agentcat.Options{}}
	agentcat.RegisterServer(server, instance)
	t.Cleanup(func() { agentcat.UnregisterServer(server) })
	server.AddReceivingMiddleware(newTrackingMiddleware(server, "proj_precision", DefaultOptions(), mock.publish, nil))

	cs := connectClient(t, server)
	if _, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "big_numbers",
		Arguments: map[string]any{
			"exact":   int64(9007199254740993),
			"huge":    uint64(12345678901234567890),
			"context": "testing precision",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("call must succeed, got tool error: %v", res.Content)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(receivedRaw, &fields); err != nil {
		t.Fatalf("handler raw arguments: %v", err)
	}
	if _, has := fields["context"]; has {
		t.Error("context must be stripped before the handler")
	}
	if got := string(fields["exact"]); got != "9007199254740993" {
		t.Errorf("handler raw exact = %s, want 9007199254740993", got)
	}
	if got := string(fields["huge"]); got != "12345678901234567890" {
		t.Errorf("handler raw huge = %s, want 12345678901234567890", got)
	}
	if received.Exact != 9007199254740993 {
		t.Errorf("handler bound exact = %d, want 9007199254740993", received.Exact)
	}
	if received.Huge != 12345678901234567890 {
		t.Errorf("handler bound huge = %d, want 12345678901234567890", received.Huge)
	}
}

// TestRebuildSurvivesGCWhileHandlerLive is the anti-elision pin for the weak
// rebuild holder: as long as the composed middleware handler is alive, the
// holder must be too — a future edit that stops routing dispatch through the
// holder would let GC collect it under a live server and silently kill
// rebuild-on-demand (this test's registries would stay nil).
func TestRebuildSurvivesGCWhileHandlerLive(t *testing.T) {
	mock := &mockPublisher{}
	var dispatched *mcp.CallToolRequest
	next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "tools/list":
			return &mcp.ListToolsResult{Tools: []*mcp.Tool{{
				Name: "gc_tool",
				InputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"payload": map[string]any{"type": "string"}},
				},
			}}}, nil
		case "tools/call":
			dispatched, _ = req.(*mcp.CallToolRequest)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		}
		return nil, nil
	}
	handler, instance := buildDirectCallHandler(t, nil, mock, next)

	for range 5 {
		runtime.GC()
	}

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name:      "gc_tool",
		Arguments: json.RawMessage(`{"context":"gc probe","payload":"keep"}`),
	}}
	if _, err := handler(context.Background(), "tools/call", req); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if instance.Registries.Load() == nil {
		t.Fatal("rebuild-on-demand must still work after GC while the handler is live")
	}
	var got map[string]any
	if err := json.Unmarshal(dispatched.Params.Arguments, &got); err != nil {
		t.Fatalf("dispatched arguments: %v", err)
	}
	if _, has := got["context"]; has {
		t.Error("context must be stripped: the rebuilt registries were not applied")
	}
	if got["payload"] != "keep" {
		t.Errorf("customer argument lost: %v", got)
	}
}
