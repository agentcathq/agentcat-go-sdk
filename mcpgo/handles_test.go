package mcpgo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// ── mint + mint-back ─────────────────────────────────────────────────────────

func TestCallMintsSessionAndMintsBack(t *testing.T) {
	h := newSpyHarness(t, nil)

	// First call WITHOUT session_id.
	res, evt := h.call("add_todo", map[string]any{
		"title":   "hi",
		"context": "adding my first todo",
	})

	// 1. The wire response carries the leading mint-back text block as its
	// FIRST content element…
	if got := firstText(t, res); !strings.HasPrefix(got, "[session_id issued") {
		t.Errorf("missing mint-back block, first content = %q", got)
	}
	// …and the published event does NOT (wire-only discipline).
	resp := fmt.Sprint(evt.Response)
	if strings.Contains(resp, "[session_id") {
		t.Errorf("mint-back leaked into the published event response: %s", resp)
	}
	if strings.Contains(resp, "mcp_session") {
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
	if !strings.Contains(firstText(t, res), evt.GetSessionId()) {
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
	_, evt2 := h.call("add_todo", map[string]any{"title": "again", "context": "x"})
	if evt2.GetSessionId() == evt.GetSessionId() {
		t.Error("two mint calls must mint distinct sessions (stateless per call)")
	}
}

// TestStartSentinelMintsLikeOmission pins the start value's contract: it
// resolves exactly like a first call that sent nothing — mint, issued block —
// and the literal value is never adopted as a session.
func TestStartSentinelMintsLikeOmission(t *testing.T) {
	h := newSpyHarness(t, nil)

	res, evt := h.call("add_todo", map[string]any{"title": "hi", "session_id": "start"})
	if got := firstText(t, res); !strings.HasPrefix(got, "[session_id issued") {
		t.Errorf("start must be issued a session, first content = %q", got)
	}
	if !strings.HasPrefix(evt.GetSessionId(), "ses_") {
		t.Errorf("sessionId = %q, want a minted ses_ id", evt.GetSessionId())
	}
	if evt.Tags == nil || (*evt.Tags)["agentcat_session_id_source"] != "minted" {
		t.Errorf("session source tag must be minted, got %v", evt.Tags)
	}
	// Case variants are graced (the schema pattern documents lowercase).
	_, evt2 := h.call("add_todo", map[string]any{"title": "hi", "session_id": " START "})
	if evt2.Tags == nil || (*evt2.Tags)["agentcat_session_id_source"] != "minted" {
		t.Errorf("case-variant start must still mint, got %v", evt2.Tags)
	}
	// The raw record keeps the sentinel verbatim.
	args, _ := evt.Parameters["arguments"].(map[string]any)
	if args["session_id"] != "start" {
		t.Errorf("raw arguments must record the sentinel, got %v", args)
	}
}

func TestCallWithSuppliedSessionEchoesWithoutReminting(t *testing.T) {
	h := newSpyHarness(t, nil)

	res, evt := h.call("add_todo", map[string]any{"title": "hi", "session_id": sid("supplied")})
	if evt.GetSessionId() != sid("supplied") {
		t.Errorf("supplied session must be trusted verbatim, got %q", evt.GetSessionId())
	}
	if evt.Tags == nil || (*evt.Tags)["agentcat_session_id_source"] != "supplied" {
		t.Errorf("tag must be supplied, got %v", evt.Tags)
	}
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok && strings.Contains(tc.Text, "session_id issued") {
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
	h := newSpyHarness(t, nil)

	res, evt := h.call("always_fails", map[string]any{})
	if !res.IsError {
		t.Fatal("fixture tool must return an IsError result")
	}
	if got := firstText(t, res); !strings.Contains(got, "session_id issued") {
		t.Errorf("mint-back must be prepended to isError results too, got %q", got)
	}
	if evt.IsError == nil || !*evt.IsError {
		t.Error("event must record the tool error")
	}
	if evt.Error == nil {
		t.Fatal("event must carry the tool's error details")
	}
	if msg, _ := evt.Error["message"].(string); !strings.Contains(msg, "intentional failure") {
		t.Errorf("error message must come from the result's text blocks, got %v", evt.Error)
	}
	// Wire-only: the decorated text must not become part of the recorded error.
	if msg, _ := evt.Error["message"].(string); strings.Contains(msg, "[session_id") {
		t.Errorf("mint-back leaked into the recorded error: %v", evt.Error)
	}
}

// TestMintBackReachesToolsThatReturnNoContent pins the mint-back on tools that
// return structuredContent only. mcp-go hands the middleware the handler's
// result verbatim — nil content stays nil — so a mint-back guarded on
// non-nil content would never reach such agents and every call would mint a
// fresh session, fragmenting the analytics session.
func TestMintBackReachesToolsThatReturnNoContent(t *testing.T) {
	h := newSpyHarness(t, nil)

	res, evt := h.call("structured_only", map[string]any{})

	if got := firstText(t, res); !strings.Contains(got, "session_id issued") {
		t.Errorf("mint-back must be prepended to content-less results too, got %q", got)
	}
	if !strings.Contains(firstText(t, res), evt.GetSessionId()) {
		t.Error("mint-back text does not name the event's session id")
	}
	// Wire-only: the published event carries the customer's original result.
	resp := fmt.Sprint(evt.Response)
	if strings.Contains(resp, "[session_id") || strings.Contains(resp, "mcp_session") {
		t.Errorf("decoration leaked into the published event response: %s", resp)
	}
}

// ── structured mirror ────────────────────────────────────────────────────────

func TestStructuredMirror(t *testing.T) {
	h := newSpyHarness(t, nil)

	// List first so the output-injection registry exists.
	h.listTools()

	// structured_only declares a plain-object output schema, so the mirror
	// re-confirms supplied handles on every response.
	res, evt := h.call("structured_only", map[string]any{"session_id": sid("s")})
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %T, want a JSON object", res.StructuredContent)
	}
	mi, ok := sc["mcp_session"].(map[string]any)
	if !ok {
		t.Fatalf("mirror missing from structuredContent: %v", sc)
	}
	if mi["session_id"] != sid("s") {
		t.Errorf("mirror must re-confirm supplied handles on every response, got %v", mi["session_id"])
	}
	if status, _ := mi["status"].(string); status != "active" {
		t.Errorf("supplied handles get the active status, got %q", status)
	}
	// The customer's own structured payload survives alongside the mirror.
	if sc["text"] != "structured" {
		t.Errorf("customer structured payload lost: %v", sc)
	}
	// Event purity: the published response carries no mirror.
	if strings.Contains(fmt.Sprint(evt.Response), "mcp_session") {
		t.Error("mirror leaked into the published event")
	}

	// A minted call mirrors the new session with the full issued-session copy.
	res2, evt2 := h.call("structured_only", map[string]any{})
	sc2, _ := res2.StructuredContent.(map[string]any)
	mi2, ok := sc2["mcp_session"].(map[string]any)
	if !ok {
		t.Fatalf("mirror missing on minted call: %v", sc2)
	}
	if mi2["session_id"] != evt2.GetSessionId() {
		t.Errorf("mirror session %v != event session %q", mi2["session_id"], evt2.GetSessionId())
	}
	if status, _ := mi2["status"].(string); status != "issued" {
		t.Errorf("minted mirror must carry the issued status, got %q", status)
	}
}

// TestStructuredMirrorPreservesNumbersOnTheWire pins number fidelity through
// the mirror: mirroring re-encodes the customer's structuredContent, and
// decoding numbers as float64 would silently change large integers (and break
// a "type":"integer" output schema).
func TestStructuredMirrorPreservesNumbersOnTheWire(t *testing.T) {
	// Injecting the mirror means editing structuredContent, and only mcp-go
	// v0.56.0+ carries the original bytes alongside the decoded value to
	// re-marshal from. Below that the decoded map is all there is, so a value
	// above 2^53 rounds through float64 before AgentCat ever sees it —
	// upstream's own behavior on those versions. See mcpgo/compat.go.
	if rawStructuredField() == nil {
		t.Skip("mcp-go < v0.56.0 does not preserve structured content bytes")
	}

	h := newSpyHarness(t, nil)
	h.listTools()

	res, _ := h.call("big_numbers", map[string]any{"session_id": sid("big")})

	wire, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal wire result: %v", err)
	}
	if !strings.Contains(string(wire), "mcp_session") {
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
	h := newSpyHarness(t, nil)
	h.listTools()

	// structured_no_schema returns structuredContent but declares no output
	// schema: the mirror is gated on the output-injection registry.
	res, _ := h.call("structured_no_schema", map[string]any{"session_id": sid("n")})
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("fixture must return structuredContent, got %T", res.StructuredContent)
	}
	if _, has := sc["mcp_session"]; has {
		t.Error("mirror must be gated on the output-injection registry")
	}
}

// ── rebuild on demand ────────────────────────────────────────────────────────

// TestRegistriesRebuiltOnDemand drives a tools/call as the very first request
// on a fresh instance (no tools/list ever served). The registries must be
// rebuilt on the call path, which is observable: without them ShouldMirror
// fails open and would decorate a tool that declares no output schema.
func TestRegistriesRebuiltOnDemand(t *testing.T) {
	h := newSpyHarness(t, nil)

	instance := getMCPcat(h.Server)
	if instance == nil {
		t.Fatal("harness server is not tracked")
	}
	if instance.Registries.Load() != nil {
		t.Fatal("precondition: no tools/list has been served yet")
	}

	res, _ := h.call("structured_no_schema", map[string]any{"session_id": sid("r")})

	if instance.Registries.Load() == nil {
		t.Error("registries must be rebuilt on demand on the call path")
	}
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("fixture must return structuredContent, got %T", res.StructuredContent)
	}
	if _, has := sc["mcp_session"]; has {
		t.Error("rebuilt registries must gate the mirror off for schema-less tools")
	}
}

// ── hook mode ────────────────────────────────────────────────────────────────

func TestHookMode(t *testing.T) {
	opts := &Options{
		ResolveSessionID: func(ctx context.Context, request mcp.CallToolRequest) (string, error) {
			return "customer-abc", nil
		},
	}
	h := newSpyHarnessWithProject(t, opts, "proj_1")

	res, evt := h.call("structured_only", map[string]any{})
	// Golden vector: DeriveSessionID("customer-abc", "proj_1"), pinned cross-SDK.
	if evt.GetSessionId() != "ses_2cOHEO0LYGADMzRvWTXXVbbgxgm" {
		t.Errorf("hook derivation drifted: %q", evt.GetSessionId())
	}
	if evt.Tags == nil || (*evt.Tags)["agentcat_session_id_source"] != "hook" {
		t.Errorf("tag must be hook, got %v", evt.Tags)
	}
	// No session instructions ever: neither text block nor structured mirror.
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok && strings.Contains(tc.Text, "[session_id") {
			t.Errorf("hook mode shows no session instructions ever, got %q", tc.Text)
		}
	}
	if sc, ok := res.StructuredContent.(map[string]any); ok {
		if _, has := sc["mcp_session"]; has {
			t.Error("hook mode must not mirror handles")
		}
	}

	// And the listed schemas have no session_id param anywhere.
	for _, tool := range h.listTools().Tools {
		if _, has := tool.InputSchema.Properties["session_id"]; has {
			t.Errorf("hook mode injects no session_id (tool %s)", tool.Name)
		}
	}
}

func TestHookModeErrorMintsSilently(t *testing.T) {
	opts := &Options{
		ResolveSessionID: func(ctx context.Context, request mcp.CallToolRequest) (string, error) {
			return "", fmt.Errorf("upstream unavailable")
		},
	}
	h := newSpyHarnessWithProject(t, opts, "proj_1")

	res, evt := h.call("add_todo", map[string]any{"title": "x"})
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
		if tc, ok := c.(mcp.TextContent); ok && strings.Contains(tc.Text, "[session_id") {
			t.Errorf("hook mode shows no session instructions even on silent mint, got %q", tc.Text)
		}
	}
}

// TestHookModeReceivesTheToolCallRequest pins the hook's argument: mcp-go's
// value-typed CallToolRequest, so customers can key correlation off the call.
func TestHookModeReceivesTheToolCallRequest(t *testing.T) {
	var seen string
	opts := &Options{
		ResolveSessionID: func(ctx context.Context, request mcp.CallToolRequest) (string, error) {
			seen = request.Params.Name
			return "customer-abc", nil
		},
	}
	h := newSpyHarnessWithProject(t, opts, "proj_1")

	h.call("add_todo", map[string]any{"title": "x"})
	if seen != "add_todo" {
		t.Errorf("hook received tool name %q, want add_todo", seen)
	}
}

// ── agent tracking ───────────────────────────────────────────────────────────

func TestAgentTracking(t *testing.T) {
	h := newSpyHarness(t, &Options{EnableAgentTracking: true})

	_, evt := h.call("add_todo", map[string]any{
		"title": "x", "agent_id": "opus|cc|k3n9x", "session_id": sid("t"),
	})
	tags := *evt.Tags
	if tags["agentcat_agent_id"] != "opus|cc|k3n9x" || tags["agentcat_agent_id_source"] != "supplied" {
		t.Errorf("agent tags wrong: %v", tags)
	}

	// Omission never rejects the call and adds no agent tags.
	res, evt2 := h.call("add_todo", map[string]any{"title": "x", "session_id": sid("t")})
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

func TestAgentTrackingOffIgnoresSuppliedAgentID(t *testing.T) {
	h := newSpyHarness(t, nil)

	_, evt := h.call("add_todo", map[string]any{"title": "x", "agent_id": "sneaky"})
	if _, ok := (*evt.Tags)["agentcat_agent_id"]; ok {
		t.Error("agent_id must be ignored when agent tracking is off")
	}
}

// ── only tool-call events ────────────────────────────────────────────────────

func TestOnlyToolCallEventsPublished(t *testing.T) {
	h := newSpyHarness(t, nil)
	ctx := context.Background()

	// The todo fixture serves resources and prompts — the richest non-tool
	// surface — and the harness already completed an initialize handshake.
	h.listTools()
	if _, err := h.Client.ListResources(ctx, mcp.ListResourcesRequest{}); err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	readReq := mcp.ReadResourceRequest{}
	readReq.Params.URI = "todo://about"
	if _, err := h.Client.ReadResource(ctx, readReq); err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	promptReq := mcp.GetPromptRequest{}
	promptReq.Params.Name = "summarize_todos"
	promptReq.Params.Arguments = map[string]string{"style": "brief"}
	if _, err := h.Client.GetPrompt(ctx, promptReq); err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	h.call("list_todos", map[string]any{})

	settleForAbsentEvents()

	events := h.Mock.getEvents()
	for _, e := range events {
		if e.EventType == nil || *e.EventType != "mcp:tools/call" {
			t.Errorf("unexpected event types %v — v2 publishes only tool calls", eventTypes(events))
			break
		}
	}
	if got := len(filterEvents(events, "mcp:tools/call")); got != 1 {
		t.Errorf("expected exactly 1 tool-call event, got %d (of %d total)", got, len(events))
	}
}

// ── stripping ────────────────────────────────────────────────────────────────

func TestHandlerReceivesStrippedArgsEndToEnd(t *testing.T) {
	h := newSpyHarness(t, &Options{EnableAgentTracking: true})

	// List first so stripping is registry-driven, not heuristic.
	h.listTools()

	res, evt := h.call("echo_args", map[string]any{
		"payload": "keep", "session_id": sid("e"), "agent_id": "a1", "context": "why I called",
	})

	var echoed map[string]any
	first, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("echo content = %T, want text", res.Content[0])
	}
	if err := json.Unmarshal([]byte(first.Text), &echoed); err != nil {
		t.Fatalf("echoed args: %v (%q)", err, first.Text)
	}
	for _, injected := range []string{"session_id", "agent_id", "context"} {
		if _, has := echoed[injected]; has {
			t.Errorf("handler must not see the injected %s", injected)
		}
	}
	if echoed["payload"] != "keep" {
		t.Errorf("real argument lost in stripping: %v", echoed)
	}

	// The event records the raw, unstripped request.
	args, _ := evt.Parameters["arguments"].(map[string]any)
	if args["session_id"] != sid("e") || args["context"] != "why I called" || args["payload"] != "keep" {
		t.Errorf("event must record raw args, got %v", args)
	}
	if args["agent_id"] != "a1" {
		t.Errorf("event must record the raw agent_id, got %v", args)
	}
}

// TestStrippingPreservesArgumentNumberFidelity pins that rebuilding the
// dispatched arguments does not round large integers: mcp-go preserves the
// original argument bytes precisely so handlers using BindArguments see the
// customer's literal value.
func TestStrippingPreservesArgumentNumberFidelity(t *testing.T) {
	// mcp-go only began preserving the original argument bytes in v0.56.0.
	// Below that the decoded map is the sole source of truth and a value above
	// 2^53 rounds through float64 — upstream's own behavior on those versions,
	// which AgentCat neither causes nor can repair. See mcpgo/compat.go.
	if !rawArgumentsAvailable() {
		t.Skip("mcp-go < v0.56.0 does not preserve raw argument bytes")
	}

	h := newSpyHarness(t, nil)
	h.listTools()

	// Drive raw JSON so the big integer never passes through a Go float64.
	raw := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"echo_raw",` +
		`"arguments":{"session_id":"` + sid("big") + `","id":1234567890123456789}}}`
	response := h.Server.HandleMessage(context.Background(), []byte(raw))
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !strings.Contains(string(encoded), "1234567890123456789") {
		t.Errorf("stripping corrupted the handler's raw arguments: %s", encoded)
	}
	if strings.Contains(string(encoded), sid("big")) {
		t.Errorf("session_id must be stripped before dispatch: %s", encoded)
	}
}

func TestGetMoreToolsCallStripsHandlesKeepsContext(t *testing.T) {
	h := newSpyHarness(t, nil)

	// No tools/list first: the registries are rebuilt on the call path, and
	// get_more_tools owns its context parameter so it must survive.
	res, evt := h.call("get_more_tools", map[string]any{
		"session_id": sid("gmt"), "context": "need an image tool",
	})
	if res.IsError {
		t.Fatalf("get_more_tools rejected the call: %q", resultText(res))
	}
	if evt.GetSessionId() != sid("gmt") {
		t.Errorf("supplied session must attribute the event, got %q", evt.GetSessionId())
	}
	if evt.UserIntent == nil || *evt.UserIntent != "need an image tool" {
		t.Errorf("get_more_tools context is the user intent, got %v", evt.UserIntent)
	}
}

// ── client identity ladder ───────────────────────────────────────────────────

func TestClientIdentityFromMetaPassthrough(t *testing.T) {
	h := newSpyHarness(t, nil)

	// Reserved _meta keys on the tools/call params: mcp-go passes _meta
	// through untouched, so a 2026 client's identity wins the ladder.
	_, evt := h.callWithMeta("add_todo", map[string]any{"title": "x"}, map[string]any{
		"io.modelcontextprotocol/clientInfo":      map[string]any{"name": "future-client", "version": "9.9"},
		"io.modelcontextprotocol/protocolVersion": "2026-07-28",
	})
	if evt.ClientName == nil || evt.ClientVersion == nil {
		t.Fatalf("client identity missing: name=%v version=%v", evt.ClientName, evt.ClientVersion)
	}
	if *evt.ClientName != "future-client" || *evt.ClientVersion != "9.9" {
		t.Errorf("meta passthrough must win the identity ladder, got %q/%q", *evt.ClientName, *evt.ClientVersion)
	}
	if evt.Tags == nil || (*evt.Tags)["agentcat_protocol_version"] != "2026-07-28" {
		t.Errorf("protocol version tag missing: %v", evt.Tags)
	}
}

func TestClientIdentityFallsBackToInitialize(t *testing.T) {
	h := newSpyHarness(t, nil)

	// No _meta keys: identity comes from the session's initialize capture.
	_, evt := h.call("add_todo", map[string]any{"title": "x"})

	// mcp-go's IN-PROCESS session only began surfacing the initialize client
	// info through server.SessionWithClientInfo in v0.57.0. The interface
	// itself exists in every supported version and fillClientIdentity's type
	// assertion is version-independent, so on older mcp-go this rung simply
	// has nothing to report and identity is legitimately absent. Assert the
	// VALUE when the rung answers; never assert that it must.
	if evt.ClientName != nil {
		if *evt.ClientName != "handles-test-client" {
			t.Errorf("initialize capture reported the wrong client identity: %q", *evt.ClientName)
		}
		if evt.ClientVersion == nil || *evt.ClientVersion != "0.1.0" {
			t.Errorf("client name resolved but version did not, got %v", evt.ClientVersion)
		}
	} else if evt.ClientVersion != nil {
		t.Errorf("client version without a name is never valid, got %v", evt.ClientVersion)
	}

	if evt.Tags != nil {
		if _, has := (*evt.Tags)["agentcat_protocol_version"]; has {
			t.Error("no protocol version tag without the reserved _meta key")
		}
	}
}

// TestEventCarriesServerInfo pins the server name/version stamped on every
// tool-call event (mcp-go exposes them only as unexported fields).
func TestEventCarriesServerInfo(t *testing.T) {
	h := newSpyHarness(t, nil)

	_, evt := h.call("add_todo", map[string]any{"title": "x"})
	if evt.ServerName == nil || *evt.ServerName != "todo-server" {
		t.Errorf("server name = %v, want todo-server", evt.ServerName)
	}
	if evt.ServerVersion == nil || *evt.ServerVersion != "1.0.0" {
		t.Errorf("server version = %v, want 1.0.0", evt.ServerVersion)
	}
}

// ── identity ─────────────────────────────────────────────────────────────────

// TestIdentifyStampsEveryToolCallEvent pins per-call identity: Identify runs on
// each tool call and stamps only that event; no separate identify event exists.
func TestIdentifyStampsEveryToolCallEvent(t *testing.T) {
	calls := 0
	h := newSpyHarness(t, &Options{
		Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
			calls++
			return &agentcat.UserIdentity{
				UserID:   fmt.Sprintf("u%d", calls),
				UserName: "Ada",
				UserData: map[string]any{"call": calls},
			}
		},
	})

	_, evt1 := h.call("add_todo", map[string]any{"title": "a"})
	_, evt2 := h.call("add_todo", map[string]any{"title": "b"})

	if evt1.IdentifyActorGivenId == nil || *evt1.IdentifyActorGivenId != "u1" {
		t.Errorf("first event actor = %v, want u1", evt1.IdentifyActorGivenId)
	}
	if evt2.IdentifyActorGivenId == nil || *evt2.IdentifyActorGivenId != "u2" {
		t.Errorf("second event actor = %v, want u2", evt2.IdentifyActorGivenId)
	}
	// Per-call verbatim: no merge across calls.
	if evt2.IdentifyData["call"] != 2 {
		t.Errorf("identity must be per-call verbatim, got %v", evt2.IdentifyData)
	}
	for _, e := range h.Mock.getEvents() {
		if e.EventType != nil && *e.EventType == "agentcat:identify" {
			t.Error("v2 publishes no separate identify event")
		}
	}
}

// TestIdentifyEmptyUserIDDoesNotStampActor pins that a non-nil identity with an
// empty UserID leaves the actor unset rather than stamping an empty actor.
func TestIdentifyEmptyUserIDDoesNotStampActor(t *testing.T) {
	h := newSpyHarness(t, &Options{
		Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
			return &agentcat.UserIdentity{UserName: "anonymous"}
		},
	})

	_, evt := h.call("add_todo", map[string]any{"title": "x"})
	if evt.IdentifyActorGivenId != nil {
		t.Errorf("empty UserID must not stamp an actor, got %q", *evt.IdentifyActorGivenId)
	}
	if evt.IdentifyActorName != nil {
		t.Errorf("identity without a user id must not stamp a name, got %q", *evt.IdentifyActorName)
	}
}

// ── DisableTracing on the call path ──────────────────────────────────────────

func TestDisableTracingNoMintBackNoEvents(t *testing.T) {
	h := newSpyHarness(t, &Options{DisableTracing: true})

	// context is still injected (its own flag allows it), so it must still be
	// stripped before the handler sees it.
	res, err := h.Client.CallTool(context.Background(), func() mcp.CallToolRequest {
		req := mcp.CallToolRequest{}
		req.Params.Name = "echo_args"
		req.Params.Arguments = map[string]any{"payload": "quiet", "context": "why"}
		return req
	}())
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatal("call must succeed with tracing disabled")
	}
	var echoed map[string]any
	if err := json.Unmarshal([]byte(resultText(res)), &echoed); err != nil {
		t.Fatalf("echoed args: %v (%q)", err, resultText(res))
	}
	if _, has := echoed["context"]; has {
		t.Error("context must still be stripped when tracing is disabled")
	}
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok && strings.Contains(tc.Text, "[session_id") {
			t.Error("no mint-back when tracing is disabled")
		}
	}

	settleForAbsentEvents()
	if events := h.Mock.getEvents(); len(events) != 0 {
		t.Errorf("expected 0 events with DisableTracing, got %d", len(events))
	}
}
