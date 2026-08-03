package mcpgo

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk/v2"
)

// listTools performs a real tools/list through the harness client.
func listTools(t *testing.T, h *testHarness) *mcp.ListToolsResult {
	t.Helper()
	result, err := h.Client.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	return result
}

// findListedTool returns the named tool from a tools/list result.
func findListedTool(t *testing.T, result *mcp.ListToolsResult, name string) mcp.Tool {
	t.Helper()
	for _, tool := range result.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found in list result", name)
	return mcp.Tool{}
}

func containsStr(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

func TestListInjectionAddsHandlesAndRegistries(t *testing.T) {
	h := newHarness(t, &Options{EnableAgentTracking: true})

	result := listTools(t, h)

	tool := findListedTool(t, result, "add_todo")
	for _, p := range []string{"session_id", "agent_id", "context"} {
		if _, ok := tool.InputSchema.Properties[p]; !ok {
			t.Errorf("missing injected param %s", p)
		}
	}
	if containsStr(tool.InputSchema.Required, "session_id") {
		t.Error("session_id must never be required")
	}
	if !containsStr(tool.InputSchema.Required, "agent_id") {
		t.Error("agent_id must be required when agent tracking is on")
	}
	if !containsStr(tool.InputSchema.Required, "context") {
		t.Error("context must be required")
	}
	if !containsStr(tool.InputSchema.Required, "title") {
		t.Error("customer-required param title must survive injection")
	}

	// get_more_tools receives handles but keeps its own context.
	gmt := findListedTool(t, result, "get_more_tools")
	if _, ok := gmt.InputSchema.Properties["session_id"]; !ok {
		t.Error("get_more_tools must receive session_id")
	}
	ctxProp, _ := gmt.InputSchema.Properties["context"].(map[string]any)
	if ctxProp == nil || ctxProp["description"] != agentcat.GetMoreToolsContextDescription {
		t.Errorf("get_more_tools must keep its own context description, got %v", ctxProp)
	}
	if gmt.Annotations.ReadOnlyHint == nil || !*gmt.Annotations.ReadOnlyHint {
		t.Error("get_more_tools must carry readOnlyHint")
	}

	// Registries stored on the instance.
	inst := agentcat.GetInstance(h.Server)
	if inst == nil {
		t.Fatal("no instance registered for server")
	}
	reg := inst.Registries.Load()
	if reg == nil {
		t.Fatal("registries not stored after tools/list")
	}
	if got := reg.InjectedParams["add_todo"]; len(got) != 3 {
		t.Errorf("expected 3 injected params for add_todo, got %v", got)
	}
	if got := reg.InjectedParams["get_more_tools"]; containsStr(got, "context") {
		t.Errorf("context must not be recorded as injected for get_more_tools, got %v", got)
	}
}

func TestListInjectionHookModeOmitsSessionID(t *testing.T) {
	h := newHarness(t, &Options{
		EnableAgentTracking: true,
		ResolveSessionID: func(ctx context.Context, request mcp.CallToolRequest) (string, error) {
			return "external-session", nil
		},
	})

	result := listTools(t, h)

	for _, tool := range result.Tools {
		if _, has := tool.InputSchema.Properties["session_id"]; has {
			t.Errorf("tool %s: session_id must not be injected in hook mode", tool.Name)
		}
	}

	// agent_id and context still inject in hook mode.
	tool := findListedTool(t, result, "add_todo")
	if _, has := tool.InputSchema.Properties["agent_id"]; !has {
		t.Error("agent_id must still be injected in hook mode with agent tracking on")
	}
	if _, has := tool.InputSchema.Properties["context"]; !has {
		t.Error("context must still be injected in hook mode")
	}
	if !containsStr(tool.InputSchema.Required, "agent_id") {
		t.Error("agent_id must be required in hook mode with agent tracking on")
	}
}

// TestListInjectionDisableTracingOmitsHandles verifies BuildInjectConfig's
// InjectHandles gating end to end: with tracing disabled, tools/list injects
// NO handles (session_id or agent_id, even with agent tracking on) while context
// injection still honors its own flag.
func TestListInjectionDisableTracingOmitsHandles(t *testing.T) {
	h := newHarness(t, &Options{
		DisableTracing:      true,
		EnableAgentTracking: true,
		// DisableToolCallContext stays false: context must still inject.
	})

	result := listTools(t, h)

	for _, tool := range result.Tools {
		if _, has := tool.InputSchema.Properties["session_id"]; has {
			t.Errorf("tool %s: session_id must not be injected with tracing disabled", tool.Name)
		}
		if _, has := tool.InputSchema.Properties["agent_id"]; has {
			t.Errorf("tool %s: agent_id must not be injected with tracing disabled", tool.Name)
		}
	}

	tool := findListedTool(t, result, "add_todo")
	if _, has := tool.InputSchema.Properties["context"]; !has {
		t.Error("context must still be injected when only tracing is disabled")
	}
}

// TestListInjectionDisableToolCallContextKeepsHandles is the mirror case:
// context injection off, handles still on.
func TestListInjectionDisableToolCallContextKeepsHandles(t *testing.T) {
	h := newHarness(t, &Options{DisableToolCallContext: true})

	result := listTools(t, h)

	tool := findListedTool(t, result, "add_todo")
	if _, has := tool.InputSchema.Properties["context"]; has {
		t.Error("context must not be injected when DisableToolCallContext is set")
	}
	if _, has := tool.InputSchema.Properties["session_id"]; !has {
		t.Error("session_id must still be injected when only context injection is disabled")
	}

	// get_more_tools declares context itself: its own parameter survives.
	gmt := findListedTool(t, result, "get_more_tools")
	if _, has := gmt.InputSchema.Properties["context"]; !has {
		t.Error("get_more_tools must keep its own context parameter")
	}
}

func TestListInjectionDoesNotMutateRegisteredTools(t *testing.T) {
	h := newHarness(t, nil)

	first := listTools(t, h)
	second := listTools(t, h) // second list must not double-inject

	tool := findListedTool(t, second, "add_todo")
	sessionProp, _ := tool.InputSchema.Properties["session_id"].(map[string]any)
	if sessionProp == nil {
		t.Fatal("session_id missing from second list")
	}
	if _, nested := sessionProp["properties"]; nested {
		t.Error("double injection detected: session_id schema has nested properties")
	}

	// required must not accumulate duplicates across lists.
	seen := map[string]int{}
	for _, r := range tool.InputSchema.Required {
		seen[r]++
		if seen[r] > 1 {
			t.Errorf("required entry %q duplicated: injection ran against mutated state", r)
		}
	}

	// The injection is deterministic from unmutated registered tools: both
	// lists must advertise identical schemas.
	firstTool := findListedTool(t, first, "add_todo")
	if !reflect.DeepEqual(firstTool.InputSchema, tool.InputSchema) {
		t.Errorf("second list diverged from first:\nfirst:  %+v\nsecond: %+v", firstTool.InputSchema, tool.InputSchema)
	}
}

// TestApplyInjectedToolsKeepsRegisteredSchemaUntouched drives the
// normalize → build → apply pipeline against a listed tool whose structured
// schema shares its Properties map with the server's registered tool (mcp-go
// hands hooks value copies, and a map copies by reference), and verifies only
// the advertised copy changes.
func TestApplyInjectedToolsKeepsRegisteredSchemaUntouched(t *testing.T) {
	shared := map[string]any{"query": map[string]any{"type": "string"}}
	result := &mcp.ListToolsResult{Tools: []mcp.Tool{{
		Name: "shared_tool",
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: shared,
			Required:   []string{"query"},
		},
	}}}

	normalized := normalizeMCPGoTools(result.Tools)
	injected, reg := agentcat.BuildInjectedTools(agentcat.BuildInjectConfig(&agentcat.Options{}, false), normalized)
	applyInjectedToolsMCPGo(result, injected)

	if _, leaked := shared["session_id"]; leaked || len(shared) != 1 {
		t.Errorf("registered properties map mutated: %v", shared)
	}
	// mcp-go refuses to marshal a tool carrying both schema forms.
	if result.Tools[0].InputSchema.Type != "" {
		t.Error("structured InputSchema must be zeroed once RawInputSchema is set")
	}
	if !strings.Contains(string(result.Tools[0].RawInputSchema), `"session_id"`) {
		t.Errorf("advertised copy missing injected session_id: %s", result.Tools[0].RawInputSchema)
	}
	if _, err := json.Marshal(result.Tools[0]); err != nil {
		t.Errorf("advertised tool no longer marshals: %v", err)
	}
	if len(reg.InjectedParams["shared_tool"]) == 0 {
		t.Errorf("registry recorded no injected params: %+v", reg)
	}
}

// TestListInjectionHookIsSafeWithoutInstanceOrResult covers the hook's guards:
// an untracked server and a nil result are both no-ops, never panics.
//
// Both guards sit BEHIND the server-identity guard, which stands down unless
// the in-flight request belongs to this server — and only mcp-go itself puts
// a server in the request context. So the hook is driven through the server's
// own message entry point, and the nil-result case reuses the context that
// entry point built, or neither guard would ever be reached.
func TestListInjectionHookIsSafeWithoutInstanceOrResult(t *testing.T) {
	hooks := &server.Hooks{}
	mcpServer := server.NewMCPServer(
		"untracked-server",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithHooks(hooks),
	)
	mcpServer.AddTool(
		mcp.NewTool("test_tool", mcp.WithDescription("A tool")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	)
	registerListInjection(hooks, mcpServer, false)

	if len(hooks.OnAfterListTools) != 1 {
		t.Fatalf("expected exactly 1 AfterListTools hook, got %d", len(hooks.OnAfterListTools))
	}

	// A second hook captures the request context mcp-go builds, which is the
	// only way to obtain one that satisfies the server-identity guard.
	var listCtx context.Context
	hooks.AddAfterListTools(func(ctx context.Context, id any, message *mcp.ListToolsRequest, result *mcp.ListToolsResult) {
		listCtx = ctx
	})

	// Untracked server: the identity guard passes, the instance guard stands
	// down, and the advertised schema is left exactly as registered.
	if got := listToolRawJSON(t, mcpServer, "test_tool"); strings.Contains(got, `"session_id"`) {
		t.Errorf("untracked server must not be injected into: %s", got)
	}
	if listCtx == nil {
		t.Fatal("the AfterListTools hook never ran; the identity guard was never exercised")
	}
	if server.ServerFromContext(listCtx) != mcpServer {
		t.Fatal("captured context does not carry this server; the identity guard would reject it")
	}

	// Tracked now, so the instance guard passes and injection happens — which
	// proves the untouched schema above was the instance guard's doing, not
	// the identity guard's.
	instance := newTestInstance(mcpServer, "proj_test", DefaultOptions())
	agentcat.RegisterServer(mcpServer, instance)
	t.Cleanup(func() { unregisterServer(mcpServer) })
	if got := listToolRawJSON(t, mcpServer, "test_tool"); !strings.Contains(got, `"session_id"`) {
		t.Errorf("tracked server must be injected into: %s", got)
	}

	// Nil result on a tracked server: past the identity and instance guards,
	// so this reaches the result guard. It must not panic, and it must not
	// disturb the registries the real list recorded.
	before := instance.Registries.Load()
	hooks.OnAfterListTools[0](listCtx, "req-nil", &mcp.ListToolsRequest{}, nil)
	if instance.Registries.Load() != before {
		t.Error("a nil result must leave the registries alone")
	}
}

func TestListInjectionPreservesCustomerParamOrder(t *testing.T) {
	h := newHarness(t, &Options{EnableAgentTracking: true})
	registerOrderedTool(h.Server)

	raw := listToolRawJSON(t, h.Server, "ordered_tool")

	zebra := strings.Index(raw, `"zebra"`)
	apple := strings.Index(raw, `"apple"`)
	session := strings.Index(raw, `"session_id"`)
	agent := strings.Index(raw, `"agent_id"`)
	contextIdx := strings.Index(raw, `"context"`)
	if zebra < 0 || apple < 0 || session < 0 || agent < 0 || contextIdx < 0 {
		t.Fatalf("expected customer and injected params in schema: %s", raw)
	}
	if !(zebra < apple && apple < session) {
		t.Errorf("property order broken: %s", raw)
	}
	if !(session < agent && agent < contextIdx) {
		t.Errorf("injected param order broken (want session_id, agent_id, context): %s", raw)
	}
}
