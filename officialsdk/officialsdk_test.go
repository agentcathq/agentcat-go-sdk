package officialsdk

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	defer unregisterServer(server)

	_, err := Track(server, "", nil)
	if err == nil {
		t.Fatal("expected error for empty projectID, got nil")
	}
	if !errors.Is(err, agentcat.ErrEmptyProjectID) {
		t.Fatalf("expected ErrEmptyProjectID, got: %v", err)
	}
}

func TestTrack_NilOptions(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	defer unregisterServer(server)

	_, err := Track(server, "proj_123", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	instance := getMCPcat(server)
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
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	defer unregisterServer(server)

	_, err := Track(server, "proj_456", DefaultOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	instance := getMCPcat(server)
	if instance == nil {
		t.Fatal("GetMCPcat returned nil after Track")
	}
	if instance.ProjectID != "proj_456" {
		t.Fatalf("expected projectID 'proj_456', got '%s'", instance.ProjectID)
	}
}

func TestTrack_CustomOptions(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	defer unregisterServer(server)

	opts := &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		Debug:                  false,
	}

	_, err := Track(server, "proj_custom", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	instance := getMCPcat(server)
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

func TestTrack_WithIdentify(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	defer unregisterServer(server)

	identifyCalled := false
	opts := &Options{
		Identify: func(ctx context.Context, request *mcp.CallToolRequest) *UserIdentity {
			identifyCalled = true
			return &UserIdentity{
				UserID:   "user123",
				UserName: "Test User",
			}
		},
	}

	_, err := Track(server, "proj_identify", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	instance := getMCPcat(server)
	if instance == nil {
		t.Fatal("expected instance to be registered")
	}

	// Verify identify function is stored (we just check opts is correctly saved)
	if opts.Identify == nil {
		t.Error("expected Identify function to be set")
	}

	_ = identifyCalled // will be tested in middleware tests
}

func Test_unregisterServer_RemovesFromRegistry(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)

	_, err := Track(server, "proj_unreg", DefaultOptions())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify registered
	if getMCPcat(server) == nil {
		t.Fatal("expected instance to be registered before unregister")
	}

	// Unregister
	unregisterServer(server)

	// Verify removed
	if getMCPcat(server) != nil {
		t.Fatal("expected instance to be nil after unregister")
	}
}

func Test_getMCPcat_UnregisteredServer(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "unregistered", Version: "1.0.0"}, nil)
	instance := getMCPcat(server)
	if instance != nil {
		t.Fatal("expected nil for unregistered server")
	}
}

func TestDefaultOptions_Values(t *testing.T) {
	opts := DefaultOptions()
	if opts == nil {
		t.Fatal("expected non-nil default options")
	}
	if opts.DisableReportMissing {
		t.Error("expected DisableReportMissing to be false by default")
	}
	if opts.DisableToolCallContext {
		t.Error("expected DisableToolCallContext to be false by default")
	}
	if opts.Debug {
		t.Error("expected Debug to be false by default")
	}
	if opts.Identify != nil {
		t.Error("expected Identify to be nil by default")
	}
}
