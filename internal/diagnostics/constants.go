// Package diagnostics mirrors the SDK's internal operational logs to MCPCat's
// monitoring as OTLP/HTTP log records. It sends only operational metadata —
// never event payloads or user data. On by default; opt out via the
// DisableDiagnostics option or the DISABLE_DIAGNOSTICS env var.
package diagnostics

import "time"

const (
	// DiagnosticsScopeName is the OTLP instrumentation scope name.
	DiagnosticsScopeName = "mcpcat-diagnostics"

	// DefaultDiagnosticsEndpoint is the OTLP collector base URL. The POST path
	// /v1/logs is appended by resolveEndpoint. Override with DIAGNOSTICS_ENDPOINT.
	DefaultDiagnosticsEndpoint = "https://otel.agentcat.com"

	// DefaultDiagnosticsToken is the public shared ingestion key — NOT a secret.
	// It ships in the binary to deter drive-by traffic, paired with a server-side
	// rate limit, and must match the collector's bearer token. Override with
	// DIAGNOSTICS_TOKEN.
	DefaultDiagnosticsToken = "dgk_sdk_diag_3f9a2c7e1b8d4065af2e9c1d7b6a4f80"

	// sdkModulePath is this SDK's module path, used to resolve its own version.
	sdkModulePath = "go.agentcat.com/sdk"

	// maxBuffer caps buffered log records (drop-oldest on overflow).
	maxBuffer = 1000
)

// batchFlush is the delay before a buffered batch is flushed.
const batchFlush = 2 * time.Second
