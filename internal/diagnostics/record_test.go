package diagnostics

import (
	"strings"
	"testing"

	"go.agentcat.com/sdk/v2/internal/logging"
)

func TestBuildRecord_Severity(t *testing.T) {
	ResetForTest()
	cases := []struct {
		level    logging.Level
		wantNum  int
		wantText string
	}{
		{logging.LevelError, 17, "ERROR"},
		{logging.LevelWarn, 13, "WARN"},
		{logging.LevelInfo, 9, "INFO"},
	}
	for _, c := range cases {
		rec := BuildRecordForTest(c.level, "the message")
		if rec.SeverityNumber != c.wantNum || rec.SeverityText != c.wantText {
			t.Errorf("level %v: got (%d,%q), want (%d,%q)",
				c.level, rec.SeverityNumber, rec.SeverityText, c.wantNum, c.wantText)
		}
		if rec.Body.StringValue != "the message" {
			t.Errorf("body = %q, want %q", rec.Body.StringValue, "the message")
		}
		if len(rec.Attributes) != 0 {
			t.Errorf("per-record attributes must be empty before Init, got %d", len(rec.Attributes))
		}
		if rec.TimeUnixNano == "" {
			t.Error("timeUnixNano must be set")
		}
		for _, r := range rec.TimeUnixNano {
			if r < '0' || r > '9' {
				t.Errorf("timeUnixNano must be decimal digits, got %q", rec.TimeUnixNano)
				break
			}
		}
	}
}

// TestBuildRecord_VersionAttrsAfterInit verifies every record built after Init
// carries the three version attributes (mirroring the resource block, so
// record-level-only backend views still see them), and that ResetForTest
// clears them again.
func TestBuildRecord_VersionAttrsAfterInit(t *testing.T) {
	// Force-enable: the SDK auto-disables under go test. Init sends nothing
	// itself; only capture/Flush POST, and neither runs here.
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	ResetForTest()
	defer ResetForTest()

	Init("proj_1", false, "officialsdk", "github.com/modelcontextprotocol/go-sdk")

	rec := BuildRecordForTest(logging.LevelInfo, "msg")
	got := map[string]string{}
	for _, a := range rec.Attributes {
		got[a.Key] = a.Value.StringValue
	}
	for _, key := range []string{
		"agentcat.sdk.version",
		"agentcat.mcp_sdk.version",
		"process.runtime.version",
	} {
		if got[key] == "" {
			t.Errorf("per-record attribute %q missing or empty; attrs: %+v", key, rec.Attributes)
		}
	}
	if !strings.HasPrefix(got["process.runtime.version"], "go") {
		t.Errorf("process.runtime.version = %q, want go1.x form", got["process.runtime.version"])
	}

	ResetForTest()
	if rec := BuildRecordForTest(logging.LevelInfo, "msg"); len(rec.Attributes) != 0 {
		t.Errorf("per-record attributes must be empty after ResetForTest, got %+v", rec.Attributes)
	}
}
