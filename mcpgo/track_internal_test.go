package mcpgo

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestTrackInternal_DefaultOptionsUsedWhenNil verifies that when Track is called
// with nil options, the default options are applied (DisableReportMissing=false,
// DisableToolCallContext=false).
func TestTrackInternal_DefaultOptionsUsedWhenNil(t *testing.T) {
	mcpServer := server.NewMCPServer("test-defaults", "1.0.0", server.WithToolCapabilities(true))

	_, err := Track(mcpServer, "proj_defaults", nil)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	defer unregisterServer(mcpServer)

	instance := getMCPcat(mcpServer)
	if instance == nil {
		t.Fatal("Expected non-nil MCPcat instance after Track with nil options")
	}

	if instance.Options.DisableReportMissing {
		t.Error("expected DisableReportMissing to be false by default")
	}

	if instance.Options.DisableToolCallContext {
		t.Error("expected DisableToolCallContext to be false by default")
	}
}

// TestTrackInternal_CustomOptionsPreserved verifies that custom option values
// passed to Track are preserved in the registered MCPcat instance.
func TestTrackInternal_CustomOptionsPreserved(t *testing.T) {
	mcpServer := server.NewMCPServer("test-custom-opts", "1.0.0", server.WithToolCapabilities(true))

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Debug:                  true,
	}

	_, err := Track(mcpServer, "proj_custom", opts)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	defer unregisterServer(mcpServer)

	instance := getMCPcat(mcpServer)
	if instance == nil {
		t.Fatal("Expected non-nil MCPcat instance after Track with custom options")
	}

	if !instance.Options.DisableReportMissing {
		t.Error("expected DisableReportMissing to be true")
	}

	if !instance.Options.DisableToolCallContext {
		t.Error("expected DisableToolCallContext to be true")
	}

	if !instance.Options.Debug {
		t.Error("Expected Debug=true (custom), got false")
	}
}

// TestTrackInternal_RegistersGetMoreToolsWhenEnabled verifies that when
// DisableReportMissing=false (the default), the "get_more_tools" tool is registered on the server.
func TestTrackInternal_RegistersGetMoreToolsWhenEnabled(t *testing.T) {
	mcpServer := server.NewMCPServer("test-get-more-tools", "1.0.0", server.WithToolCapabilities(true))

	opts := &Options{}

	_, err := Track(mcpServer, "proj_get_more", opts)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	defer unregisterServer(mcpServer)

	tools := mcpServer.ListTools()
	found := false
	for _, serverTool := range tools {
		if serverTool.Tool.Name == "get_more_tools" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected 'get_more_tools' to be registered when DisableReportMissing=false")
	}
}

// TestTrackInternal_DoesNotRegisterGetMoreToolsWhenDisabled verifies that when
// DisableReportMissing=true, the "get_more_tools" tool is NOT registered.
func TestTrackInternal_DoesNotRegisterGetMoreToolsWhenDisabled(t *testing.T) {
	mcpServer := server.NewMCPServer("test-no-get-more-tools", "1.0.0", server.WithToolCapabilities(true))

	opts := &Options{
		DisableReportMissing: true,
	}

	_, err := Track(mcpServer, "proj_no_get_more", opts)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	defer unregisterServer(mcpServer)

	tools := mcpServer.ListTools()
	for _, serverTool := range tools {
		if serverTool.Tool.Name == "get_more_tools" {
			t.Error("Expected 'get_more_tools' NOT to be registered when DisableReportMissing=true")
		}
	}
}

// TestTrackInternal_HooksMergeWithExisting verifies that when the user provides
// their own hooks, Track appends AgentCat's list-injection hook to the same
// struct rather than replacing it: both fire on one request.
func TestTrackInternal_HooksMergeWithExisting(t *testing.T) {
	userHookRan := false
	hooks := &server.Hooks{}
	hooks.AddBeforeAny(func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
		userHookRan = true
	})

	mcpServer := server.NewMCPServer(
		"test-hooks-merge",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithHooks(hooks),
	)

	opts := &Options{
		DisableReportMissing: true,
	}

	_, err := Track(mcpServer, "proj_hooks_merge", opts)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	defer unregisterServer(mcpServer)

	if len(hooks.OnAfterListTools) != 1 {
		t.Errorf("expected AgentCat's list hook on the user's struct, got %d", len(hooks.OnAfterListTools))
	}

	mcpServer.HandleMessage(
		context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`),
	)

	if !userHookRan {
		t.Error("the user's BeforeAny hook must still run after Track")
	}
	if instance := getMCPcat(mcpServer); instance == nil || instance.Registries.Load() == nil {
		t.Error("AgentCat's list injection did not run off the user's hooks")
	}
}

// TestTrackInternal_RegistryContainsCorrectInstance verifies that after Track,
// GetMCPcat returns an instance with the correct ProjectID.
func TestTrackInternal_RegistryContainsCorrectInstance(t *testing.T) {
	mcpServer := server.NewMCPServer("test-registry", "1.0.0", server.WithToolCapabilities(true))

	projectID := "proj_registry_check_123"
	_, err := Track(mcpServer, projectID, nil)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	defer unregisterServer(mcpServer)

	instance := getMCPcat(mcpServer)
	if instance == nil {
		t.Fatal("Expected non-nil MCPcat instance from GetMCPcat")
	}

	if instance.ProjectID != projectID {
		t.Errorf("Expected ProjectID %q, got %q", projectID, instance.ProjectID)
	}
}
