package agentcat

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"go.agentcat.com/sdk/internal/logging"
)

// captureLogger swaps the singleton logger's writer to buf for the duration of the test.
func captureLogger(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	lg := logging.New()
	lg.SwapWriterForTest(log.New(buf, "[MCPCat] ", log.LstdFlags))
}

func TestInitDiagnostics_EmitsStartBeacon(t *testing.T) {
	t.Setenv("DISABLE_DIAGNOSTICS", "1") // keep network off; only assert the log line
	logging.ResetForTesting()
	ResetDiagnosticsForTest()
	defer ResetDiagnosticsForTest()

	var buf bytes.Buffer
	captureLogger(t, &buf)

	InitDiagnostics("proj_abc", true, "officialsdk", "github.com/modelcontextprotocol/go-sdk")

	out := buf.String()
	if !strings.Contains(out, "MCPCat setup started") ||
		!strings.Contains(out, "proj_abc") ||
		!strings.Contains(out, "integration officialsdk") {
		t.Fatalf("start beacon missing/incomplete: %q", out)
	}
}

func TestInitDiagnostics_TelemetryOnlyLabel(t *testing.T) {
	t.Setenv("DISABLE_DIAGNOSTICS", "1")
	logging.ResetForTesting()
	ResetDiagnosticsForTest()
	defer ResetDiagnosticsForTest()

	var buf bytes.Buffer
	captureLogger(t, &buf)

	InitDiagnostics("", true, "mcpgo", "github.com/mark3labs/mcp-go")

	if !strings.Contains(buf.String(), "(telemetry-only)") {
		t.Fatalf("empty projectID must render as (telemetry-only): %q", buf.String())
	}
}

func TestLogSetupComplete_MetadataOnly(t *testing.T) {
	t.Setenv("DISABLE_DIAGNOSTICS", "1")
	logging.ResetForTesting()
	ResetDiagnosticsForTest()
	defer ResetDiagnosticsForTest()

	var buf bytes.Buffer
	captureLogger(t, &buf)

	LogSetupComplete("proj_abc", &Options{DisableToolCallContext: false, DisableReportMissing: true})

	out := buf.String()
	if !strings.Contains(out, "MCPCat setup complete") ||
		!strings.Contains(out, "proj_abc") ||
		!strings.Contains(out, "context=true") ||
		!strings.Contains(out, "report_missing=false") {
		t.Fatalf("complete beacon wrong: %q", out)
	}
}

func TestLogSetupFailed_IsError(t *testing.T) {
	t.Setenv("DISABLE_DIAGNOSTICS", "1")
	logging.ResetForTesting()
	ResetDiagnosticsForTest()
	defer ResetDiagnosticsForTest()

	var buf bytes.Buffer
	captureLogger(t, &buf)

	LogSetupFailed("projectID must not be empty")

	out := buf.String()
	if !strings.Contains(out, "ERROR:") || !strings.Contains(out, "MCPCat setup failed") {
		t.Fatalf("failure must log ERROR: %q", out)
	}
}
