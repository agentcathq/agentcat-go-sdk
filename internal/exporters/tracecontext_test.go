package exporters

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"testing"
)

func TestTraceID_Deterministic(t *testing.T) {
	a := TraceID("ses_abc123")
	b := TraceID("ses_abc123")
	if a != b {
		t.Errorf("TraceID not deterministic: %q != %q", a, b)
	}
	if len(a) != 32 {
		t.Errorf("TraceID length = %d, want 32", len(a))
	}

	sum := sha256.Sum256([]byte("ses_abc123"))
	want := hex.EncodeToString(sum[:])[:32]
	if a != want {
		t.Errorf("TraceID = %q, want first 32 hex chars of SHA-256 (%q)", a, want)
	}
}

func TestTraceID_EmptyIsRandom(t *testing.T) {
	a := TraceID("")
	b := TraceID("")
	if len(a) != 32 || len(b) != 32 {
		t.Fatalf("random trace IDs must be 32 hex chars, got %d and %d", len(a), len(b))
	}
	if a == b {
		t.Error("random trace IDs should differ between calls")
	}
}

func TestSpanID_Deterministic(t *testing.T) {
	a := SpanID("evt_xyz789")
	b := SpanID("evt_xyz789")
	if a != b {
		t.Errorf("SpanID not deterministic: %q != %q", a, b)
	}
	if len(a) != 16 {
		t.Errorf("SpanID length = %d, want 16", len(a))
	}

	sum := sha256.Sum256([]byte("evt_xyz789"))
	want := hex.EncodeToString(sum[:])[:16]
	if a != want {
		t.Errorf("SpanID = %q, want first 16 hex chars of SHA-256 (%q)", a, want)
	}
}

func TestSpanID_EmptyIsRandom(t *testing.T) {
	a := SpanID("")
	b := SpanID("")
	if len(a) != 16 || len(b) != 16 {
		t.Fatalf("random span IDs must be 16 hex chars, got %d and %d", len(a), len(b))
	}
	if a == b {
		t.Error("random span IDs should differ between calls")
	}
}

func TestDatadogTraceID_DecimalOfLow64Bits(t *testing.T) {
	sessionID := "ses_abc123"
	h := TraceID(sessionID)
	wantVal, err := strconv.ParseUint(h[16:32], 16, 64)
	if err != nil {
		t.Fatalf("ParseUint: %v", err)
	}

	got := DatadogTraceID(sessionID)
	if got != strconv.FormatUint(wantVal, 10) {
		t.Errorf("DatadogTraceID = %q, want %q", got, strconv.FormatUint(wantVal, 10))
	}
	if !regexp.MustCompile(`^\d+$`).MatchString(got) {
		t.Errorf("DatadogTraceID must be decimal, got %q", got)
	}
}

func TestDatadogSpanID_DecimalOfSpanID(t *testing.T) {
	eventID := "evt_xyz789"
	wantVal, err := strconv.ParseUint(SpanID(eventID), 16, 64)
	if err != nil {
		t.Fatalf("ParseUint: %v", err)
	}

	got := DatadogSpanID(eventID)
	if got != strconv.FormatUint(wantVal, 10) {
		t.Errorf("DatadogSpanID = %q, want %q", got, strconv.FormatUint(wantVal, 10))
	}
}
