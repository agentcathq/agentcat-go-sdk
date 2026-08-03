package mcpgo

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk/v2"
)

func TestTrack_NilServer(t *testing.T) {
	_, err := Track(nil, "proj_123", nil)
	if err == nil {
		t.Fatal("expected error for nil server, got nil")
	}
	if !errors.Is(err, agentcat.ErrNilServer) {
		t.Fatalf("expected ErrNilServer, got: %v", err)
	}
}

func TestTrack_EmptyProjectID(t *testing.T) {
	mcpServer := server.NewMCPServer("test-server", "1.0.0")
	defer unregisterServer(mcpServer)

	_, err := Track(mcpServer, "", nil)
	if err == nil {
		t.Fatal("expected error for empty projectID, got nil")
	}
	if !errors.Is(err, agentcat.ErrEmptyProjectID) {
		t.Fatalf("expected ErrEmptyProjectID, got: %v", err)
	}
}

func TestTrack_NilOptions(t *testing.T) {
	mcpServer := server.NewMCPServer("test-server", "1.0.0")
	defer unregisterServer(mcpServer)

	_, err := Track(mcpServer, "proj_123", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	instance := getMCPcat(mcpServer)
	if instance == nil {
		t.Fatal("expected instance to be registered")
	}
	if instance.ProjectID != "proj_123" {
		t.Fatalf("expected projectID 'proj_123', got '%s'", instance.ProjectID)
	}
	// Default options should be applied
	if instance.Options == nil {
		t.Fatal("expected options to be set")
	}
	if instance.Options.DisableReportMissing {
		t.Error("expected DisableReportMissing to be false by default")
	}
	if instance.Options.DisableToolCallContext {
		t.Error("expected DisableToolCallContext to be false by default")
	}
}

func TestTrack_RegistersServer(t *testing.T) {
	mcpServer := server.NewMCPServer("test-server", "1.0.0")
	defer unregisterServer(mcpServer)

	_, err := Track(mcpServer, "proj_456", DefaultOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	instance := getMCPcat(mcpServer)
	if instance == nil {
		t.Fatal("GetMCPcat returned nil after Track")
	}
	if instance.ProjectID != "proj_456" {
		t.Fatalf("expected projectID 'proj_456', got '%s'", instance.ProjectID)
	}
}

// TestTrack_CustomHooksPreserved verifies that hooks the customer registered
// at construction survive Track: AgentCat composes with them via GetHooks()
// instead of replacing them.
func TestTrack_CustomHooksPreserved(t *testing.T) {
	customHookCalled := false
	customHooks := &server.Hooks{}
	customHooks.AddBeforeAny(func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
		customHookCalled = true
	})

	mcpServer := server.NewMCPServer(
		"test-server",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithHooks(customHooks),
	)
	defer unregisterServer(mcpServer)

	_, err := Track(mcpServer, "proj_789", DefaultOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Drive a real request: both the customer's hook and AgentCat's list
	// injection must run off the same Hooks struct.
	mcpServer.HandleMessage(
		context.Background(),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`),
	)

	if !customHookCalled {
		t.Error("customer BeforeAny hook did not run after Track")
	}
	if instance := getMCPcat(mcpServer); instance == nil || instance.Registries.Load() == nil {
		t.Error("AgentCat list injection did not run off the customer's hooks")
	}
}

// TestTrack_IsIdempotentPerServer verifies that tracking the same server twice
// installs one tool middleware, not two: a second installation would append a
// second mint-back block to every wire response and publish a second event.
func TestTrack_IsIdempotentPerServer(t *testing.T) {
	mcpServer, _ := CreateTodoServerSimple()
	instance := newTestInstance(mcpServer, "proj_twice", DefaultOptions())
	agentcat.RegisterServer(mcpServer, instance)
	t.Cleanup(func() { unregisterServer(mcpServer) })

	mock := &mockPublisher{}
	installTracking(mcpServer, instance, DefaultOptions(), mock.publish)

	// A second Track on the same server must be a no-op.
	if _, err := Track(mcpServer, "proj_twice", DefaultOptions()); err != nil {
		t.Fatalf("second Track failed: %v", err)
	}

	result := callToolRaw(t, mcpServer, "add_todo", map[string]any{"title": "once"})

	mintBacks := 0
	for _, content := range result.Content {
		if tc, ok := content.(mcp.TextContent); ok && strings.Contains(tc.Text, "session_id issued") {
			mintBacks++
		}
	}
	if mintBacks != 1 {
		t.Errorf("expected exactly 1 mint-back block on the wire, got %d", mintBacks)
	}
	if got := len(filterEvents(mock.getEvents(), "mcp:tools/call")); got != 1 {
		t.Errorf("expected exactly 1 published event, got %d", got)
	}
}

func TestTrack_CustomOptions(t *testing.T) {
	mcpServer := server.NewMCPServer("test-server", "1.0.0")
	defer unregisterServer(mcpServer)

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	}

	_, err := Track(mcpServer, "proj_custom", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	instance := getMCPcat(mcpServer)
	if instance == nil {
		t.Fatal("expected instance to be registered")
	}
	if !instance.Options.DisableReportMissing {
		t.Error("expected DisableReportMissing to be true")
	}
	if !instance.Options.DisableToolCallContext {
		t.Error("expected DisableToolCallContext to be true")
	}
}

func Test_unregisterServer_RemovesFromRegistry(t *testing.T) {
	mcpServer := server.NewMCPServer("test-server", "1.0.0")

	_, err := Track(mcpServer, "proj_unreg", DefaultOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify registered
	if getMCPcat(mcpServer) == nil {
		t.Fatal("expected instance to be registered before unregister")
	}

	// Unregister
	unregisterServer(mcpServer)

	// Verify removed
	if getMCPcat(mcpServer) != nil {
		t.Fatal("expected instance to be nil after unregister")
	}
}

func Test_getMCPcat_UnregisteredServer(t *testing.T) {
	mcpServer := server.NewMCPServer("unregistered", "1.0.0")
	instance := getMCPcat(mcpServer)
	if instance != nil {
		t.Fatal("expected nil for unregistered server")
	}
}
