package mcpgo

// Tests restored from mcpcat_test.go (root package).
// Many of these tests referenced internal/registry directly or the old MCPcat struct
// which no longer exists in the new structure. Only tests that are portable have
// been adapted.

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
)

func TestMcpcatAPI_TrackNilServer(t *testing.T) {
	_, err := Track(nil, "proj_id", nil)
	if err == nil {
		t.Error("Expected error when tracking with nil server")
	}
}

func TestMcpcatAPI_TrackEmptyProjectID(t *testing.T) {
	s := server.NewMCPServer("test", "1.0.0")
	_, err := Track(s, "", nil)
	if err == nil {
		t.Error("Expected error for empty project ID")
	}
}

func TestMcpcatAPI_TrackNilOptions(t *testing.T) {
	s := server.NewMCPServer("test", "1.0.0")
	_, err := Track(s, "proj_test", nil)
	if err != nil {
		t.Fatalf("Track with nil options failed: %v", err)
	}
	defer unregisterServer(s)

	instance := getMCPcat(s)
	if instance == nil {
		t.Fatal("Expected MCPcat instance after Track")
	}
	if instance.ProjectID != "proj_test" {
		t.Errorf("Expected project ID 'proj_test', got '%s'", instance.ProjectID)
	}
	if instance.Options.DisableReportMissing {
		t.Error("expected DisableReportMissing to be false by default")
	}
	if instance.Options.DisableToolCallContext {
		t.Error("expected DisableToolCallContext to be false by default")
	}
}

func TestMcpcatAPI_TrackWithOptions(t *testing.T) {
	s := server.NewMCPServer("test", "1.0.0")
	opts := &Options{
		Debug:                true,
		DisableReportMissing: true,
	}
	_, err := Track(s, "proj_custom", opts)
	if err != nil {
		t.Fatalf("Track with options failed: %v", err)
	}
	defer unregisterServer(s)

	instance := getMCPcat(s)
	if instance == nil {
		t.Fatal("Expected MCPcat instance")
	}
	if !instance.Options.DisableReportMissing {
		t.Error("Expected DisableReportMissing=true")
	}
}

func TestMcpcatAPI_TrackWithExistingHooks(t *testing.T) {
	s := server.NewMCPServer("test", "1.0.0")
	hooks := &server.Hooks{}
	_, err := Track(s, "proj_hooks", &Options{Hooks: hooks})
	if err != nil {
		t.Fatalf("Track with hooks failed: %v", err)
	}
	defer unregisterServer(s)

	instance := getMCPcat(s)
	if instance == nil {
		t.Fatal("Expected MCPcat instance")
	}
	if instance.ProjectID != "proj_hooks" {
		t.Errorf("Expected project ID 'proj_hooks', got '%s'", instance.ProjectID)
	}
}

func TestMcpcatAPI_GetMCPcatNotRegistered(t *testing.T) {
	mcpServer := server.NewMCPServer("unregistered", "1.0.0")
	result := getMCPcat(mcpServer)

	if result != nil {
		t.Error("Expected nil when getting MCPcat for unregistered server")
	}
}

func TestMcpcatAPI_unregisterServer(t *testing.T) {
	mcpServer := server.NewMCPServer("test-server", "1.0.0")

	_, err := Track(mcpServer, "proj_unreg", nil)
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}

	// Verify registered
	if getMCPcat(mcpServer) == nil {
		t.Fatal("Expected instance to be registered")
	}

	// Unregister
	unregisterServer(mcpServer)

	// Verify unregistered
	if getMCPcat(mcpServer) != nil {
		t.Error("Expected nil after unregister")
	}
}

func TestMcpcatAPI_Shutdown(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Shutdown panicked: %v", r)
		}
	}()

	if err := agentcat.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown returned error: %v", err)
	}
}

func TestMcpcatAPI_ShutdownMultipleCalls(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Multiple Shutdown calls panicked: %v", r)
		}
	}()

	agentcat.Shutdown(context.Background())
	agentcat.Shutdown(context.Background())
	agentcat.Shutdown(context.Background())
}
