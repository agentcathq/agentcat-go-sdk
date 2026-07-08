package exceptions

import (
	"strings"
	"testing"
)

// unhashableErr is an error whose dynamic type cannot be used as a map key
// (slice field, value receiver). Using it as a key in the cycle-detection
// maps panics with "hash of unhashable type".
type unhashableErr struct{ parts []string }

func (e unhashableErr) Error() string { return strings.Join(e.parts, " ") }

// panickingErr panics inside its Error() method.
type panickingErr struct{}

func (panickingErr) Error() string { panic("Error() panicked") }

// wrappedUnhashableErr exercises the unwrapErrorChain seen-map with an
// unhashable error in the chain.
type wrappedUnhashableErr struct{ inner error }

func (e wrappedUnhashableErr) Error() string { return "outer: " + e.inner.Error() }
func (e wrappedUnhashableErr) Unwrap() error { return e.inner }

func TestCaptureExceptionUnhashableErrorType(t *testing.T) {
	result := CaptureException(unhashableErr{parts: []string{"boom", "boom"}})
	if result == nil {
		t.Fatal("expected non-nil result for unhashable error type")
	}
	if msg, _ := result["message"].(string); msg != "boom boom" {
		t.Errorf("expected original message to be preserved, got %q", msg)
	}
}

func TestCaptureExceptionPanickingErrorMethod(t *testing.T) {
	result := CaptureException(panickingErr{})
	if result == nil {
		t.Fatal("expected non-nil fallback result for panicking Error()")
	}
	if msg, _ := result["message"].(string); !strings.Contains(msg, "error capture failed") {
		t.Errorf("expected fallback message, got %q", msg)
	}
	if result["platform"] != "go" {
		t.Errorf("expected platform to be set on fallback, got %v", result["platform"])
	}
}

func TestCaptureExceptionUnhashableInChain(t *testing.T) {
	result := CaptureException(wrappedUnhashableErr{inner: unhashableErr{parts: []string{"inner"}}})
	if result == nil {
		t.Fatal("expected non-nil result for chain containing unhashable error")
	}
	if msg, _ := result["message"].(string); msg != "outer: inner" {
		t.Errorf("expected original message to be preserved, got %q", msg)
	}
}
