package agentcat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.agentcat.com/sdk/internal/publisher"
	"go.agentcat.com/sdk/internal/session"
)

func TestPublishCustomEvent_RequiresProjectID(t *testing.T) {
	err := PublishCustomEvent("session-123", "", nil)
	if !errors.Is(err, ErrEmptyProjectID) {
		t.Errorf("expected ErrEmptyProjectID, got %v", err)
	}
}

func TestPublishCustomEvent_UntrackedServer(t *testing.T) {
	err := PublishCustomEvent(&testServer{name: "untracked"}, "proj_123", nil)
	if !errors.Is(err, ErrServerNotTracked) {
		t.Errorf("expected ErrServerNotTracked, got %v", err)
	}
}

func TestPublishCustomEvent_InvalidTarget(t *testing.T) {
	if err := PublishCustomEvent(nil, "proj_123", nil); !errors.Is(err, ErrInvalidTarget) {
		t.Errorf("expected ErrInvalidTarget for nil, got %v", err)
	}
	if err := PublishCustomEvent(42, "proj_123", nil); !errors.Is(err, ErrInvalidTarget) {
		t.Errorf("expected ErrInvalidTarget for non-pointer, got %v", err)
	}
}

func TestPublishCustomEvent_WithSessionIDString(t *testing.T) {
	publisher.ShutdownGlobal(context.Background())
	defer publisher.ShutdownGlobal(context.Background())

	err := PublishCustomEvent("mcp-session-abc", "proj_123", &CustomEventData{
		ResourceName: "custom-action",
		Parameters:   map[string]any{"action": "feedback", "rating": 5},
		Message:      "User provided feedback",
	})
	if err != nil {
		t.Fatalf("PublishCustomEvent returned error: %v", err)
	}
}

func TestPublishCustomEvent_WithTrackedServer(t *testing.T) {
	publisher.ShutdownGlobal(context.Background())
	defer publisher.ShutdownGlobal(context.Background())

	server := &testServer{name: "tracked"}
	instance := &MCPcatInstance{
		ProjectID: "proj_123",
		Options:   &Options{},
		ServerRef: server,
		SessionID: NewSessionID(),
	}
	RegisterServer(server, instance)
	defer UnregisterServer(server)

	err := PublishCustomEvent(server, "proj_123", &CustomEventData{
		ResourceName: "feature-usage",
	})
	if err != nil {
		t.Fatalf("PublishCustomEvent returned error: %v", err)
	}
}

func TestPublishCustomEvent_TrackedServerWithTracingDisabled(t *testing.T) {
	server := &testServer{name: "tracing-disabled"}
	instance := &MCPcatInstance{
		ProjectID: "proj_123",
		Options:   &Options{DisableTracing: true},
		ServerRef: server,
		SessionID: NewSessionID(),
	}
	RegisterServer(server, instance)
	defer UnregisterServer(server)

	// Must not error and must not publish (accept-and-drop).
	if err := PublishCustomEvent(server, "proj_123", nil); err != nil {
		t.Fatalf("PublishCustomEvent returned error: %v", err)
	}
}

func TestPublishCustomEvent_DerivesDeterministicSessionID(t *testing.T) {
	// The session ID derived for a string target must match the shared
	// deterministic derivation, so custom events correlate with
	// auto-captured events for the same MCP session.
	want := session.DeriveSessionIDFromMCPSession("user-session-12345", "proj_abc123xyz")
	got := DeriveSessionID("user-session-12345", "proj_abc123xyz")
	if got != want {
		t.Errorf("DeriveSessionID = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, "ses_") {
		t.Errorf("derived session ID %q missing ses_ prefix", got)
	}
}

func TestValidateTags_Exported(t *testing.T) {
	got := ValidateTags(map[string]string{"ok": "v", "bad/key": "v"})
	if len(got) != 1 || got["ok"] != "v" {
		t.Errorf("ValidateTags = %v, want map[ok:v]", got)
	}
	if got := ValidateTags(nil); got != nil {
		t.Errorf("ValidateTags(nil) = %v, want nil", got)
	}
}
