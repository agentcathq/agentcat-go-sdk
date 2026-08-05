package agentcat

import (
	"fmt"
	"runtime"

	"go.agentcat.com/sdk/v2/internal/core"
	"go.agentcat.com/sdk/v2/internal/diagnostics"
	"go.agentcat.com/sdk/v2/internal/logging"
)

// InitDiagnostics initializes internal SDK diagnostics and emits the setup-start
// beacon. Call it early in Track — before validation — so setup failures are
// captured. Idempotent across the process.
func InitDiagnostics(projectID string, disabled bool, integration, mcpSDKPath string) {
	diagnostics.Init(projectID, disabled, integration, mcpSDKPath)
	logging.SetVersionSuffix(fmt.Sprintf(" | sdk=%s go=%s mcp=%s",
		core.GetDependencyVersion(core.SDKModulePath), runtime.Version(),
		core.GetDependencyVersion(mcpSDKPath)))
	logging.New().Infof("AgentCat setup started | project %s | integration %s",
		orTelemetryOnly(projectID), integration)
}

// LogSetupComplete emits the setup-complete beacon (metadata only).
func LogSetupComplete(projectID string, opts *Options) {
	logging.New().Infof("AgentCat setup complete | project %s | context=%t report_missing=%t exporters=%d",
		orTelemetryOnly(projectID), !opts.DisableToolCallContext, !opts.DisableReportMissing,
		len(opts.Exporters))
}

// LogSetupFailed logs a setup failure as ERROR so it surfaces in diagnostics.
func LogSetupFailed(reason string) {
	logging.New().Errorf("AgentCat setup failed | %s", reason)
}

// LogWarn writes a warning to ~/agentcat.log. Integration API for the adapter
// modules, which cannot reach internal/logging across the module boundary.
// Use it for degraded-but-safe outcomes the customer may want to know about
// (a schema AgentCat could not read, a dropped event); anything that breaks
// capture outright belongs in LogRecoveredPanic or LogSetupFailed.
func LogWarn(format string, args ...any) {
	logging.New().Warnf(format, args...)
}

// logError writes an error to ~/agentcat.log and tees it to the anonymized
// diagnostics sink, like every other Errorf in this SDK.
func logError(format string, args ...any) {
	logging.New().Errorf(format, args...)
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
