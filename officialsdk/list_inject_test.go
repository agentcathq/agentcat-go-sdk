package officialsdk

import (
	"context"
	"encoding/json"
	"reflect"
	"runtime"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// trackTestServer builds the tracked todo fixture server and connects a real
// client over in-memory transports (legacy initialize handshake pinned), so
// tools/list flows through the tracking middleware end to end. Cleanup is
// registered on t.
func trackTestServer(t *testing.T, opts *Options) (*mcp.Server, *mcp.ClientSession) {
	t.Helper()

	server, _, _ := createFullTestServerWithTracking(t, opts)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "list-inject-client", Version: "0.1.0"}, nil)
	pinLegacyInitializeHandshake(client)
	clientSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}

	t.Cleanup(func() {
		clientSession.Close()
		serverSession.Wait()
	})

	return server, clientSession
}

// listToolsResult performs a real tools/list through the client session.
func listToolsResult(t *testing.T, cs *mcp.ClientSession) *mcp.ListToolsResult {
	t.Helper()
	result, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	return result
}

// findListedTool returns the named tool from a tools/list result.
func findListedTool(t *testing.T, result *mcp.ListToolsResult, name string) *mcp.Tool {
	t.Helper()
	for _, tool := range result.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found in list result", name)
	return nil
}

// marshalToMap round-trips any schema representation into a map.
func marshalToMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	return m
}

// stringSlice converts a decoded JSON array into []string.
func stringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
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
	server, clientSession := trackTestServer(t, &Options{EnableAgentTracking: true})

	result := listToolsResult(t, clientSession)

	tool := findListedTool(t, result, "add_todo")
	schema := marshalToMap(t, tool.InputSchema)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("add_todo schema has no properties object: %v", schema)
	}
	for _, p := range []string{"session_id", "agent_id", "context"} {
		if _, ok := props[p]; !ok {
			t.Errorf("missing injected param %s", p)
		}
	}
	required := stringSlice(schema["required"])
	if !containsStr(required, "session_id") {
		t.Error("session_id must be required")
	}
	if sidProp, _ := props["session_id"].(map[string]any); sidProp == nil ||
		sidProp["pattern"] != "^(start|ses_[0-9A-Za-z]{27})$" {
		t.Errorf("session_id must declare its value pattern, got %v", props["session_id"])
	}
	if !containsStr(required, "agent_id") {
		t.Error("agent_id must be required when agent tracking is on")
	}
	if !containsStr(required, "context") {
		t.Error("context must be required")
	}
	if !containsStr(required, "title") {
		t.Error("customer-required param title must survive injection")
	}

	// get_more_tools receives handles but keeps its own context.
	gmt := findListedTool(t, result, "get_more_tools")
	gmtSchema := marshalToMap(t, gmt.InputSchema)
	gmtProps, ok := gmtSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("get_more_tools schema has no properties object: %v", gmtSchema)
	}
	if _, ok := gmtProps["session_id"]; !ok {
		t.Error("get_more_tools must receive session_id")
	}
	ctxProp, _ := gmtProps["context"].(map[string]any)
	if ctxProp == nil || ctxProp["description"] != agentcat.GetMoreToolsContextDescription {
		t.Errorf("get_more_tools must keep its own context description, got %v", ctxProp)
	}
	if gmt.Annotations == nil || !gmt.Annotations.ReadOnlyHint {
		t.Error("get_more_tools must carry readOnlyHint")
	}

	// Registries stored on the instance.
	inst := agentcat.GetInstance(server)
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
	opts := &Options{
		EnableAgentTracking: true,
		ResolveSessionID: func(ctx context.Context, req mcp.Request) (string, error) {
			return "external-session", nil
		},
	}
	_, clientSession := trackTestServer(t, opts)

	result := listToolsResult(t, clientSession)

	for _, tool := range result.Tools {
		schema := marshalToMap(t, tool.InputSchema)
		props, _ := schema["properties"].(map[string]any)
		if _, has := props["session_id"]; has {
			t.Errorf("tool %s: session_id must not be injected in hook mode", tool.Name)
		}
	}

	// agent_id and context still inject in hook mode.
	tool := findListedTool(t, result, "add_todo")
	schema := marshalToMap(t, tool.InputSchema)
	props, _ := schema["properties"].(map[string]any)
	if _, has := props["agent_id"]; !has {
		t.Error("agent_id must still be injected in hook mode with agent tracking on")
	}
	if _, has := props["context"]; !has {
		t.Error("context must still be injected in hook mode")
	}
	required := stringSlice(schema["required"])
	if !containsStr(required, "agent_id") {
		t.Error("agent_id must be required in hook mode with agent tracking on")
	}
}

// TestListInjectionDisableTracingOmitsHandles verifies BuildInjectConfig's
// InjectHandles gating end to end: with tracing disabled, tools/list injects
// NO handles (session_id or agent_id, even with agent tracking on) while context
// injection still honors its own flag.
func TestListInjectionDisableTracingOmitsHandles(t *testing.T) {
	opts := &Options{
		DisableTracing:      true,
		EnableAgentTracking: true,
		// DisableToolCallContext stays false: context must still inject.
	}
	_, clientSession := trackTestServer(t, opts)

	result := listToolsResult(t, clientSession)

	for _, tool := range result.Tools {
		schema := marshalToMap(t, tool.InputSchema)
		props, _ := schema["properties"].(map[string]any)
		if _, has := props["session_id"]; has {
			t.Errorf("tool %s: session_id must not be injected with tracing disabled", tool.Name)
		}
		if _, has := props["agent_id"]; has {
			t.Errorf("tool %s: agent_id must not be injected with tracing disabled", tool.Name)
		}
	}

	tool := findListedTool(t, result, "add_todo")
	schema := marshalToMap(t, tool.InputSchema)
	props, _ := schema["properties"].(map[string]any)
	if _, has := props["context"]; !has {
		t.Error("context must still be injected when only tracing is disabled")
	}
}

func TestListInjectionDoesNotMutateRegisteredTools(t *testing.T) {
	_, clientSession := trackTestServer(t, nil)

	first := listToolsResult(t, clientSession)
	second := listToolsResult(t, clientSession) // second list must not double-inject

	tool := findListedTool(t, second, "add_todo")
	schema := marshalToMap(t, tool.InputSchema)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("add_todo schema has no properties object: %v", schema)
	}
	sessionProp, _ := props["session_id"].(map[string]any)
	if sessionProp == nil {
		t.Fatal("session_id missing from second list")
	}
	if _, nested := sessionProp["properties"]; nested {
		t.Error("double injection detected: session_id schema has nested properties")
	}

	// required must not accumulate duplicates across lists.
	required := stringSlice(schema["required"])
	seen := map[string]int{}
	for _, r := range required {
		seen[r]++
		if seen[r] > 1 {
			t.Errorf("required entry %q duplicated: injection ran against mutated state", r)
		}
	}

	// The injection is deterministic from unmutated registered tools: both
	// lists must advertise byte-identical schemas.
	firstSchema := marshalToMap(t, findListedTool(t, first, "add_todo").InputSchema)
	if !reflect.DeepEqual(firstSchema, schema) {
		t.Errorf("second list diverged from first:\nfirst:  %v\nsecond: %v", firstSchema, schema)
	}
}

// TestHandleToolsListLeavesSharedToolsUntouched drives handleToolsList
// directly against a result whose Tools slice points at "registered" tool
// objects — exactly what the go-sdk's listTools hands the middleware — and
// verifies only copies are mutated.
func TestHandleToolsListLeavesSharedToolsUntouched(t *testing.T) {
	registered := &mcp.Tool{
		Name: "shared_tool",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []any{"query"},
		},
	}
	before, err := json.Marshal(registered.InputSchema)
	if err != nil {
		t.Fatal(err)
	}

	result := &mcp.ListToolsResult{Tools: []*mcp.Tool{registered}}
	instance := &agentcat.AgentCatInstance{Options: &agentcat.Options{}}
	handleToolsList(result, instance, agentcat.BuildInjectConfig(instance.Options, false))

	if result.Tools[0] == registered {
		t.Error("result must hold a copy, not the registered tool pointer")
	}
	after, err := json.Marshal(registered.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("registered tool schema mutated:\nbefore: %s\nafter:  %s", before, after)
	}

	// The advertised copy did get the handles.
	advertised := marshalToMap(t, result.Tools[0].InputSchema)
	props, _ := advertised["properties"].(map[string]any)
	if _, has := props["session_id"]; !has {
		t.Error("advertised copy missing injected session_id")
	}
	if reg := instance.Registries.Load(); reg == nil || len(reg.InjectedParams["shared_tool"]) == 0 {
		t.Errorf("registries not stored on instance: %+v", reg)
	}
}

// TestStashRebuildFetchesOriginalList verifies the rebuild stash issues a
// tools/list against the inner handler and returns the normalized ORIGINAL
// (uninjected) tools.
func TestStashRebuildFetchesOriginalList(t *testing.T) {
	stashRebuild(nil, nil) // nil instance must be a no-op, not a panic

	var gotMethod string
	next := func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		gotMethod = method
		return &mcp.ListToolsResult{Tools: []*mcp.Tool{
			{Name: "orig_tool", InputSchema: map[string]any{"type": "object"}},
		}}, nil
	}

	instance := &agentcat.AgentCatInstance{Options: &agentcat.Options{}}
	target := &rebuildTarget{next: next}
	stashRebuild(instance, target)
	if instance.RebuildTools == nil {
		t.Fatal("RebuildTools not stashed on instance")
	}

	tools, err := instance.RebuildTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("RebuildTools: %v", err)
	}
	// The stash holds the target only weakly; this test stands in for the
	// middleware handler that normally keeps it alive.
	runtime.KeepAlive(target)
	if gotMethod != "tools/list" {
		t.Errorf("expected tools/list against inner handler, got %q", gotMethod)
	}
	if len(tools) != 1 || tools[0].Name != "orig_tool" {
		t.Fatalf("unexpected normalized tools: %+v", tools)
	}
	if tools[0].InputSchema == nil || tools[0].InputSchema.Has("properties") {
		t.Errorf("rebuild must return the original uninjected schema, got %+v", tools[0].InputSchema)
	}
}
