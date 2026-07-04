package diagnostics

import (
	"testing"

	"go.agentcat.com/sdk/internal/logging"
)

func TestBuildRecord_Severity(t *testing.T) {
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
			t.Errorf("per-record attributes must be empty, got %d", len(rec.Attributes))
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
