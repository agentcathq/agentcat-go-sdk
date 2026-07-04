package mcpgo

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
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

func TestTrack_CustomHooksPreserved(t *testing.T) {
	mcpServer := server.NewMCPServer("test-server", "1.0.0")
	defer unregisterServer(mcpServer)

	customHookCalled := false
	customHooks := &server.Hooks{}
	customHooks.AddBeforeAny(func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
		customHookCalled = true
	})

	opts := DefaultOptions()
	opts.Hooks = customHooks

	_, err := Track(mcpServer, "proj_789", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify hooks were merged: custom + MCPCat hooks
	// The custom hooks struct should have at least 2 BeforeAny hooks:
	// 1 from the custom hook + 1 from MCPCat tracing
	if len(customHooks.OnBeforeAny) < 2 {
		t.Fatalf("expected at least 2 BeforeAny hooks (custom + mcpcat), got %d", len(customHooks.OnBeforeAny))
	}

	// Verify the custom hook is still there (it was the first one appended)
	_ = customHookCalled // hook reference preserved; tested via count
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
