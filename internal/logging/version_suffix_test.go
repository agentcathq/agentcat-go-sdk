package logging

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// TestSetVersionSuffix_AppendsToEveryFileLine verifies every level's file line
// carries the version suffix.
func TestSetVersionSuffix_AppendsToEveryFileLine(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := newLogger()
	defer logger.Close()

	var buf bytes.Buffer
	logger.logger = log.New(&buf, "[AgentCat] ", log.LstdFlags)

	SetVersionSuffix(" | sdk=v2.0.0 go=go1.25.5 mcp=v0.57.0")

	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")
	logger.Debug("debug message")

	for _, want := range []string{
		"INFO: info message | sdk=v2.0.0 go=go1.25.5 mcp=v0.57.0",
		"WARN: warn message | sdk=v2.0.0 go=go1.25.5 mcp=v0.57.0",
		"ERROR: error message | sdk=v2.0.0 go=go1.25.5 mcp=v0.57.0",
		"DEBUG: debug message | sdk=v2.0.0 go=go1.25.5 mcp=v0.57.0",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q:\n%s", want, buf.String())
		}
	}
}

// TestSetVersionSuffix_SinkReceivesBareMessage verifies the diagnostics sink
// gets the message without the suffix — the OTLP side carries the versions as
// structured attributes instead.
func TestSetVersionSuffix_SinkReceivesBareMessage(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	var sinkMsg string
	SetDiagnosticsSink(func(_ Level, msg string) { sinkMsg = msg })
	defer SetDiagnosticsSink(nil)

	SetGlobalDebug(true)
	logger := newLogger()
	defer logger.Close()

	var buf bytes.Buffer
	logger.logger = log.New(&buf, "[AgentCat] ", log.LstdFlags)

	SetVersionSuffix(" | sdk=v2.0.0 go=go1.25.5 mcp=v0.57.0")
	logger.Info("info message")

	if sinkMsg != "info message" {
		t.Errorf("sink message = %q, want bare %q", sinkMsg, "info message")
	}
	if !strings.Contains(buf.String(), "info message | sdk=") {
		t.Errorf("file line must still carry the suffix:\n%s", buf.String())
	}
}

// TestSetVersionSuffix_FirstWins verifies later calls are ignored, matching
// diagnostics.Init's idempotence.
func TestSetVersionSuffix_FirstWins(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := newLogger()
	defer logger.Close()

	var buf bytes.Buffer
	logger.logger = log.New(&buf, "[AgentCat] ", log.LstdFlags)

	SetVersionSuffix(" | sdk=first")
	SetVersionSuffix(" | sdk=second")
	logger.Info("message")

	out := buf.String()
	if !strings.Contains(out, "message | sdk=first") {
		t.Errorf("first suffix must win:\n%s", out)
	}
	if strings.Contains(out, "sdk=second") {
		t.Errorf("second suffix must be ignored:\n%s", out)
	}
}

// TestResetForTesting_ClearsVersionSuffix verifies the first-wins latch reopens
// after a reset.
func TestResetForTesting_ClearsVersionSuffix(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetVersionSuffix(" | sdk=stale")
	ResetForTesting()

	SetGlobalDebug(true)
	logger := newLogger()
	defer logger.Close()

	var buf bytes.Buffer
	logger.logger = log.New(&buf, "[AgentCat] ", log.LstdFlags)

	logger.Info("message")
	if strings.Contains(buf.String(), "sdk=stale") {
		t.Errorf("suffix must be cleared by ResetForTesting:\n%s", buf.String())
	}

	SetVersionSuffix(" | sdk=fresh")
	logger.Info("message")
	if !strings.Contains(buf.String(), "message | sdk=fresh") {
		t.Errorf("suffix must be settable again after ResetForTesting:\n%s", buf.String())
	}
}
