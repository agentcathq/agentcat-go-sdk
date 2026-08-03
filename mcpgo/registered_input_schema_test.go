package mcpgo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/v2"
)

// closedSchemaServer builds a server whose registered input schemas are CLOSED
// (`additionalProperties: false`) and validated before the tool handler runs —
// the exact shape mcp-go v0.57.0 added with WithInputSchemaValidation and
// WithStrictInputSchemaDefault.
func closedSchemaServer(t *testing.T) *server.MCPServer {
	t.Helper()
	mcpServer := server.NewMCPServer("closed-schema-server", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithInputSchemaValidation(),
		server.WithStrictInputSchemaDefault(),
	)
	registerHandleFixtures(mcpServer)
	return mcpServer
}

// TestClosedInputSchemaAcceptsInjectedParams is the regression gate for the
// v2 bump to mcp-go v0.57.0: a server that validates arguments against a
// CLOSED registered input schema must still accept the parameters AgentCat
// advertises. mcp-go validates against the registered schema at
// server/server.go:1991 — before Use() middleware can strip anything — so
// declaring the params on the advertised copy alone rejects every call.
func TestClosedInputSchemaAcceptsInjectedParams(t *testing.T) {
	mcpServer := closedSchemaServer(t)
	h := newSpyHarnessOn(t, mcpServer, DefaultOptions(), "proj_test")

	// A schema-following agent obeys the ADVERTISED schema, which marks
	// `context` required, so it must send it.
	advertised := listToolRawJSON(t, mcpServer, "echo_args")
	for _, want := range []string{`"session_id"`, `"context"`} {
		if !strings.Contains(advertised, want) {
			t.Fatalf("advertised schema for echo_args is missing %s: %s", want, advertised)
		}
	}
	if !strings.Contains(advertised, `"required":["context"]`) {
		t.Fatalf("advertised schema must mark context required: %s", advertised)
	}

	result, evt := h.call("echo_args", map[string]any{
		"payload": "keep", "session_id": sid("abc"), "context": "why",
	})
	if result.IsError {
		t.Fatalf("closed registered schema rejected the injected params: %q", resultText(result))
	}

	echoed := decodeEchoedArgs(t, result)
	if _, has := echoed["session_id"]; has {
		t.Errorf("handler must not see session_id: %v", echoed)
	}
	if _, has := echoed["context"]; has {
		t.Errorf("handler must not see context: %v", echoed)
	}
	if echoed["payload"] != "keep" {
		t.Errorf("real argument lost: %v", echoed)
	}

	args, _ := evt.Parameters["arguments"].(map[string]any)
	if args["session_id"] != sid("abc") {
		t.Errorf("event must record the raw session_id, got %v", args)
	}
	if args["context"] != "why" {
		t.Errorf("event must record the raw context, got %v", args)
	}
	if evt.UserIntent == nil || *evt.UserIntent != "why" {
		t.Errorf("event must record the captured intent, got %v", evt.UserIntent)
	}
	if evt.GetSessionId() != sid("abc") {
		t.Errorf("event must be filed under the supplied session, got %q", evt.GetSessionId())
	}
}

// TestClosedInputSchemaAcceptsInjectedParamsWithoutPriorList covers the same
// server when the very first request is a tools/call: the registered schema is
// declared at Track time, before any list can run.
func TestClosedInputSchemaAcceptsInjectedParamsWithoutPriorList(t *testing.T) {
	mcpServer := closedSchemaServer(t)

	opts := DefaultOptions()
	instance := newTestInstance(mcpServer, "proj_test", opts)
	agentcat.RegisterServer(mcpServer, instance)
	t.Cleanup(func() { unregisterServer(mcpServer) })
	mock := &mockPublisher{}
	installTracking(mcpServer, instance, opts, mock.publish)

	result := callToolRaw(t, mcpServer, "echo_args", map[string]any{
		"payload": "keep", "session_id": sid("first"), "context": "why",
	})
	if result.IsError {
		t.Fatalf("closed registered schema rejected the injected params: %q", resultText(result))
	}
	echoed := decodeEchoedArgs(t, result)
	if _, has := echoed["session_id"]; has {
		t.Errorf("handler must not see session_id: %v", echoed)
	}
	if _, has := echoed["context"]; has {
		t.Errorf("handler must not see context: %v", echoed)
	}
}

// TestPerToolClosedSchemaAcceptsInjectedParams covers a tool that closes its
// own schema with mcp.WithSchemaAdditionalProperties(false) on a server that
// has no strict default.
func TestPerToolClosedSchemaAcceptsInjectedParams(t *testing.T) {
	mcpServer := server.NewMCPServer("per-tool-closed-server", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithInputSchemaValidation(),
	)
	mcpServer.AddTool(
		mcp.NewTool("closed_echo",
			mcp.WithDescription("Echoes the arguments the handler received"),
			mcp.WithString("payload", mcp.Required(), mcp.Description("Anything at all")),
			mcp.WithSchemaAdditionalProperties(false),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			encoded, err := json.Marshal(request.GetArguments())
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(string(encoded)), nil
		},
	)

	h := newSpyHarnessOn(t, mcpServer, &Options{EnableAgentTracking: true}, "proj_test")
	_ = h.listTools()

	result, evt := h.call("closed_echo", map[string]any{
		"payload": "keep", "session_id": sid("pt"), "agent_id": "agent-7", "context": "why",
	})
	if result.IsError {
		t.Fatalf("per-tool closed schema rejected the injected params: %q", resultText(result))
	}
	echoed := decodeEchoedArgs(t, result)
	for _, name := range []string{"session_id", "agent_id", "context"} {
		if _, has := echoed[name]; has {
			t.Errorf("handler must not see %s: %v", name, echoed)
		}
	}
	if echoed["payload"] != "keep" {
		t.Errorf("real argument lost: %v", echoed)
	}
	if evt.Tags == nil || (*evt.Tags)["agentcat_agent_id"] != "agent-7" {
		t.Errorf("agent_id tag = %v, want agent-7", evt.Tags)
	}
}

// TestRegisteredInputDeclarationKeepsCustomerContract pins that declaring the
// injected params on the REGISTERED schema never marks them required and never
// re-opens a schema the customer closed.
func TestRegisteredInputDeclarationKeepsCustomerContract(t *testing.T) {
	mcpServer := closedSchemaServer(t)
	opts := DefaultOptions()
	instance := newTestInstance(mcpServer, "proj_test", opts)
	agentcat.RegisterServer(mcpServer, instance)
	t.Cleanup(func() { unregisterServer(mcpServer) })
	installTracking(mcpServer, instance, opts, (&mockPublisher{}).publish)

	registered := mcpServer.ListTools()["echo_args"]
	if registered == nil {
		t.Fatal("echo_args is not registered")
	}
	raw := registered.Tool.RawInputSchema
	if len(raw) == 0 {
		var err error
		raw, err = json.Marshal(registered.Tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal registered input schema: %v", err)
		}
	}

	var schema struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties any                        `json:"additionalProperties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode registered input schema %s: %v", raw, err)
	}

	for _, name := range []string{"session_id", "context"} {
		if _, has := schema.Properties[name]; !has {
			t.Errorf("registered schema must declare %q so validation passes: %s", name, raw)
		}
	}
	for _, name := range schema.Required {
		if name == "session_id" || name == "agent_id" || name == "context" {
			t.Errorf("registered schema must never REQUIRE %q: %s", name, raw)
		}
	}
	if schema.AdditionalProperties != false {
		t.Errorf("registered additionalProperties = %v, want the customer's own false: %s",
			schema.AdditionalProperties, raw)
	}
}
