package officialsdk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk"
)

// TestTrack_TelemetryOnlyModeAccepted verifies that an empty projectID is
// accepted when at least one exporter is configured (telemetry-only mode).
func TestTrack_TelemetryOnlyModeAccepted(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "telemetry-server", Version: "1.0.0"}, nil)

	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.Exporters = map[string]ExporterConfig{
		"posthog": {Type: "posthog", APIKey: "phc_test"},
	}

	shutdown, err := Track(server, "", opts)
	if err != nil {
		t.Fatalf("Track with exporters and empty projectID failed: %v", err)
	}
	t.Cleanup(func() { unregisterServer(server) })

	instance := getMCPcat(server)
	if instance == nil {
		t.Fatal("expected server to be registered")
	}
	if instance.ProjectID != "" {
		t.Errorf("ProjectID = %q, want empty in telemetry-only mode", instance.ProjectID)
	}
	if len(instance.Options.Exporters) != 1 {
		t.Errorf("Exporters on core options = %d, want 1", len(instance.Options.Exporters))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

// TestTrack_EmptyProjectIDWithoutExportersFails keeps the original validation
// when no exporters are configured.
func TestTrack_EmptyProjectIDWithoutExportersFails(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "telemetry-server", Version: "1.0.0"}, nil)

	_, err := Track(server, "", nil)
	if !errors.Is(err, agentcat.ErrEmptyProjectID) {
		t.Fatalf("Track error = %v, want ErrEmptyProjectID", err)
	}
}
