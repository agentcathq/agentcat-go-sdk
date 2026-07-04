package agentcat

import (
	"go.agentcat.com/sdk/internal/diagnostics"
	"go.agentcat.com/sdk/internal/logging"
)

// InitDiagnostics initializes internal SDK diagnostics and emits the setup-start
// beacon. Call it early in Track — before validation — so setup failures are
// captured. Idempotent across the process.
func InitDiagnostics(projectID string, disabled bool, integration, mcpSDKPath string) {
	diagnostics.Init(projectID, disabled, integration, mcpSDKPath)
	logging.New().Infof("MCPCat setup started | project %s | integration %s",
		orTelemetryOnly(projectID), integration)
}

// LogSetupComplete emits the setup-complete beacon (metadata only).
func LogSetupComplete(projectID string, opts *Options) {
	logging.New().Infof("MCPCat setup complete | project %s | context=%t report_missing=%t",
		orTelemetryOnly(projectID), !opts.DisableToolCallContext, !opts.DisableReportMissing)
}

// LogSetupFailed logs a setup failure as ERROR so it surfaces in diagnostics.
func LogSetupFailed(reason string) {
	logging.New().Errorf("MCPCat setup failed | %s", reason)
}

// ResetDiagnosticsForTest resets internal diagnostics + logging sink state. For tests.
func ResetDiagnosticsForTest() {
	diagnostics.ResetForTest()
}

func orTelemetryOnly(projectID string) string {
	if projectID == "" {
		return "(telemetry-only)"
	}
	return projectID
}
