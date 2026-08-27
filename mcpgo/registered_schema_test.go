package mcpgo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/v2"
)

// validatedOutput is the declared output shape of the validated_structured
// fixture. mcp.WithOutputSchema[T] emits "additionalProperties": false, so a
// server running WithOutputSchemaValidation rejects any result carrying a
// property the schema does not declare.
type validatedOutput struct {
	Text string `json:"text"`
}

// newValidatingServer builds a server that validates tool output against the
// REGISTERED schema (mcp-go runs that check after the tool middleware).
func newValidatingServer(t *testing.T) *server.MCPServer {
	t.Helper()

	mcpServer := server.NewMCPServer("validating-output-server", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithOutputSchemaValidation(),
	)
	mcpServer.AddTool(
		mcp.NewTool("validated_structured",
			mcp.WithDescription("Returns structuredContent under a closed output schema"),
			mcp.WithOutputSchema[validatedOutput](),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content:           []mcp.Content{mcp.NewTextContent("ok")},
				StructuredContent: map[string]any{"text": "payload"},
			}, nil
		},
	)
	return mcpServer
}

// TestMirrorSurvivesOutputSchemaValidation is the regression pin for the
// closed-output-schema break: the mirror adds mcp_session to
// structuredContent, and mcp-go validates the decorated result against the
// tool's REGISTERED output schema, so that property must be declared there.
func TestMirrorSurvivesOutputSchemaValidation(t *testing.T) {
	h := newSpyHarnessOn(t, newValidatingServer(t), nil, "proj_test")

	h.listTools()

	res, evt := h.call("validated_structured", map[string]any{"session_id": sid("v")})

	if res.IsError {
		t.Fatalf("output validation rejected the mirrored result: %q", resultText(res))
	}
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %T, want a JSON object", res.StructuredContent)
	}
	mi, ok := sc["mcp_session"].(map[string]any)
	if !ok {
		t.Fatalf("mirror missing from structuredContent: %v", sc)
	}
	if mi["session_id"] != sid("v") {
		t.Errorf("mirror session = %v, want ses_v", mi["session_id"])
	}
	if sc["text"] != "payload" {
		t.Errorf("customer payload lost: %v", sc)
	}
	// Wire-only: the published event carries the undecorated result.
	if strings.Contains(fmt.Sprint(evt.Response), "mcp_session") {
		t.Errorf("mirror leaked into the published event: %v", evt.Response)
	}
}

// TestRegisteredOutputSchemaDeclaresMirror pins the mechanism: the property is
// declared on the REGISTERED schema (what the validator reads) while the
// customer's own additionalProperties setting is left untouched.
func TestRegisteredOutputSchemaDeclaresMirror(t *testing.T) {
	mcpServer := newValidatingServer(t)
	newSpyHarnessOn(t, mcpServer, nil, "proj_test")

	registered := mcpServer.ListTools()["validated_structured"]
	if registered == nil {
		t.Fatal("fixture tool is not registered")
	}
	schema := marshalRegisteredOutputSchema(t, registered.Tool)

	props, _ := schema["properties"].(map[string]any)
	if _, has := props["mcp_session"]; !has {
		t.Errorf("registered output schema must declare mcp_session: %v", schema)
	}
	if _, has := props["text"]; !has {
		t.Errorf("customer properties must survive: %v", schema)
	}
	if additional, has := schema["additionalProperties"]; !has || additional != false {
		t.Errorf("additionalProperties must be left as the customer set it, got %v", schema)
	}
	required, _ := schema["required"].([]any)
	for _, name := range required {
		if name == "mcp_session" {
			t.Error("mcp_session must be optional, never required")
		}
	}

	// The registered INPUT schema declares the injected params too — mcp-go
	// validates arguments against it BEFORE the middleware can strip them —
	// but only ever as OPTIONAL properties, so a call that omits them still
	// passes. See registered_input_schema_test.go for the full contract.
	inputSchema := marshalRegisteredInputSchema(t, registered.Tool)
	inputProps, _ := inputSchema["properties"].(map[string]any)
	for _, injected := range []string{"session_id", "context"} {
		if _, has := inputProps[injected]; !has {
			t.Errorf("registered input schema must declare %s: %v", injected, inputSchema)
		}
	}
	inputRequired, _ := inputSchema["required"].([]any)
	for _, name := range inputRequired {
		if name == "session_id" || name == "agent_id" || name == "context" {
			t.Errorf("registered input schema must never require %v: %v", name, inputSchema)
		}
	}
}

// TestRegisteredOutputSchemaDeclarationIsIdempotent pins that repeated lists
// (each of which re-checks the registered tools) neither double-declare the
// property nor disturb the schema.
//
// Comparing the resulting schema BYTES is necessary but not sufficient: they
// are stable whether or not the declaration pass rewrites the tool every time,
// so this also asserts that the second pass produces no pending change at all.
// That is the property that keeps the pass free — see
// TestRepeatedListsDoNotNotifyToolsListChanged for what a non-free pass costs.
func TestRegisteredOutputSchemaDeclarationIsIdempotent(t *testing.T) {
	mcpServer := newValidatingServer(t)
	h := newSpyHarnessOn(t, mcpServer, nil, "proj_test")

	h.listTools()
	first := marshalRegisteredOutputSchema(t, mcpServer.ListTools()["validated_structured"].Tool)
	h.listTools()
	second := marshalRegisteredOutputSchema(t, mcpServer.ListTools()["validated_structured"].Tool)

	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Errorf("declaration is not idempotent:\n first: %s\nsecond: %s", firstJSON, secondJSON)
	}

	// The pass itself must have nothing left to do. Driven directly so the
	// assertion is about the change set, not about its visible effect.
	registered := mcpServer.ListTools()
	entries := make([]server.ServerTool, 0, len(registered))
	for _, entry := range registered {
		entries = append(entries, *entry)
	}
	cfg := agentcat.BuildInjectConfig(agentcat.GetInstance(mcpServer).Options, false)
	if decls := schemaDeclarations(entries, cfg); len(decls) != 0 {
		names := make([]string, 0, len(decls))
		for _, d := range decls {
			names = append(names, d.name)
		}
		t.Errorf("a settled declaration pass still wants to rewrite %v; every tools/list would re-register and notify", names)
	}

	// And the mirror still fires after the extra passes.
	res, _ := h.call("validated_structured", map[string]any{"session_id": sid("i")})
	if res.IsError {
		t.Fatalf("call failed after repeated declaration passes: %q", resultText(res))
	}
	if sc, ok := res.StructuredContent.(map[string]any); !ok || sc["mcp_session"] == nil {
		t.Errorf("mirror stopped firing after repeated passes: %v", res.StructuredContent)
	}
}

// TestLiveRegisteredTools_ReadsPrivateToolState is the change-detection guard
// for the sanctioned private-field access: if mcp-go renames or retypes
// MCPServer.tools or MCPServer.toolsMu, this fails loudly instead of silently
// degrading to "declarations are never applied" — which would take closed-schema
// servers back to rejecting every tool call.
func TestLiveRegisteredTools_ReadsPrivateToolState(t *testing.T) {
	mcpServer := server.NewMCPServer("tools-tripwire", "1.0.0")
	mcpServer.AddTool(
		mcp.NewTool("probe", mcp.WithDescription("A probe")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	)

	// The map never escapes the callback, so the test reaches it the same way
	// production code does — there is no other way to reach it.
	var sawProbe bool
	ok := withLiveTools(mcpServer, func(live map[string]server.ServerTool) {
		entry, has := live["probe"]
		if !has {
			return
		}
		sawProbe = true
		// It must be the LIVE map, not a copy: writing through it has to be
		// visible to the server itself, which is the whole point.
		entry.Tool.RawInputSchema = []byte(`{"type":"object","properties":{"tripwire":{"type":"string"}}}`)
		entry.Tool.InputSchema = mcp.ToolInputSchema{}
		live["probe"] = entry
	})
	if !ok {
		t.Fatal("withLiveTools failed — mcp-go's private tools/toolsMu fields may have been renamed or retyped")
	}
	if !sawProbe {
		t.Fatal("private tool map does not contain the registered tool")
	}

	if got := listToolRawJSON(t, mcpServer, "probe"); !strings.Contains(got, "tripwire") {
		t.Errorf("writing through the private map did not reach the server's own list: %s", got)
	}

	// The lock is released by the time withLiveTools returns, so the server's
	// own read path is free again — a second pass must not deadlock.
	if !withLiveTools(mcpServer, func(map[string]server.ServerTool) {}) {
		t.Error("a second withLiveTools call failed; the lock may not have been released")
	}

	if withLiveTools(nil, func(map[string]server.ServerTool) {
		t.Error("the callback must not run when the private state cannot be reached")
	}) {
		t.Error("withLiveTools(nil) must report failure, not panic")
	}
}

// TestCustomerDeclaredInstructionsPropertyIsPreserved pins that a customer who
// already declares mcp_session keeps their own definition.
func TestCustomerDeclaredInstructionsPropertyIsPreserved(t *testing.T) {
	const customerSchema = `{"type":"object","properties":{"text":{"type":"string"},"mcp_session":{"type":"string","description":"mine"}}}`

	mcpServer := server.NewMCPServer("customer-owned-server", "1.0.0",
		server.WithToolCapabilities(true),
	)
	mcpServer.AddTool(
		mcp.NewTool("owns_instructions",
			mcp.WithDescription("Declares mcp_session itself"),
			mcp.WithRawOutputSchema(json.RawMessage(customerSchema)),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{StructuredContent: map[string]any{"text": "t", "mcp_session": "mine"}}, nil
		},
	)

	h := newSpyHarnessOn(t, mcpServer, nil, "proj_test")
	h.listTools()

	schema := marshalRegisteredOutputSchema(t, mcpServer.ListTools()["owns_instructions"].Tool)
	props, _ := schema["properties"].(map[string]any)
	own, _ := props["mcp_session"].(map[string]any)
	if own["description"] != "mine" {
		t.Errorf("customer's own mcp_session definition must win: %v", props)
	}

	res, _ := h.call("owns_instructions", map[string]any{"session_id": sid("c")})
	sc, _ := res.StructuredContent.(map[string]any)
	if sc["mcp_session"] != "mine" {
		t.Errorf("customer's own value must win at runtime, got %v", sc["mcp_session"])
	}
}

// TestSessionScopedToolGetsMirrorDeclaration pins that session-scoped tools —
// which live outside the global tool map and are output-validated against
// their own registered schema — get the same declaration. Without it, a
// session tool with a closed output schema breaks exactly like a global one.
func TestSessionScopedToolGetsMirrorDeclaration(t *testing.T) {
	mcpServer := server.NewMCPServer("session-tools-server", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithOutputSchemaValidation(),
	)

	// Only the SSE and streamable-HTTP sessions support per-session tools.
	mcpClient, mock := setupSpyHTTPOn(t, mcpServer, nil)

	id := mcpClient.GetSessionId()
	if id == "" {
		t.Fatal("HTTP transport exposed no session ID")
	}
	err := mcpServer.AddSessionTools(id, server.ServerTool{
		Tool: mcp.NewTool("session_structured",
			mcp.WithDescription("A session-scoped tool with a closed output schema"),
			mcp.WithOutputSchema[validatedOutput](),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content:           []mcp.Content{mcp.NewTextContent("ok")},
				StructuredContent: map[string]any{"text": "session payload"},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("AddSessionTools: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The list is where session tools become reachable to AgentCat.
	if _, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = "session_structured"
	req.Params.Arguments = map[string]any{"session_id": sid("s")}
	res, err := mcpClient.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("output validation rejected the mirrored session-tool result: %q", resultText(res))
	}
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %T, want a JSON object", res.StructuredContent)
	}
	if _, has := sc["mcp_session"]; !has {
		t.Errorf("session-scoped tools must mirror handles too: %v", sc)
	}
	if sc["text"] != "session payload" {
		t.Errorf("customer payload lost: %v", sc)
	}

	events := filterEvents(mock.waitForEvents(1, 3*time.Second), "mcp:tools/call")
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 tool-call event, got %d", len(events))
	}
	if events[0].GetSessionId() != sid("s") {
		t.Errorf("event session = %q, want ses_s", events[0].GetSessionId())
	}
}

// TestCustomerTypedInstructionsPropertyIsNotMirrored pins the unpopulated
// case: a customer who declares `mcp_session` with a shape of their own
// must not have AgentCat's object written into it. Doing so would violate their
// declared schema — under output validation the call would fail outright.
func TestCustomerTypedInstructionsPropertyIsNotMirrored(t *testing.T) {
	const customerSchema = `{"type":"object","properties":{"text":{"type":"string"},"mcp_session":{"type":"string"}},"additionalProperties":false}`

	mcpServer := server.NewMCPServer("customer-typed-server", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithOutputSchemaValidation(),
	)
	mcpServer.AddTool(
		mcp.NewTool("typed_instructions",
			mcp.WithDescription("Declares mcp_session as a string and never fills it"),
			mcp.WithRawOutputSchema(json.RawMessage(customerSchema)),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{StructuredContent: map[string]any{"text": "t"}}, nil
		},
	)

	h := newSpyHarnessOn(t, mcpServer, nil, "proj_test")
	h.listTools()

	res, _ := h.call("typed_instructions", map[string]any{"session_id": sid("t")})
	if res.IsError {
		t.Fatalf("writing the mirror into a customer-typed property broke the call: %q", resultText(res))
	}
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %T, want a JSON object", res.StructuredContent)
	}
	if _, has := sc["mcp_session"]; has {
		t.Errorf("AgentCat must not fill a property the customer typed itself: %v", sc)
	}
}

// marshalRegisteredOutputSchema renders a registered tool's output schema
// exactly as mcp-go's validator reads it (RawOutputSchema wins when set).
func marshalRegisteredOutputSchema(t *testing.T, tool mcp.Tool) map[string]any {
	t.Helper()

	raw := []byte(tool.RawOutputSchema)
	if len(raw) == 0 {
		encoded, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("marshal output schema: %v", err)
		}
		raw = encoded
	}

	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode output schema %s: %v", raw, err)
	}
	return schema
}

// marshalRegisteredInputSchema is marshalRegisteredOutputSchema for the input
// half (RawInputSchema wins when set, exactly as mcp-go's validator reads it).
func marshalRegisteredInputSchema(t *testing.T, tool mcp.Tool) map[string]any {
	t.Helper()

	raw := []byte(tool.RawInputSchema)
	if len(raw) == 0 {
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal input schema: %v", err)
		}
		raw = encoded
	}

	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode input schema %s: %v", raw, err)
	}
	return schema
}
