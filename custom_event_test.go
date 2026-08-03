package agentcat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.agentcat.com/sdk/v2/internal/publisher"
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
	}
	RegisterServer(server, instance)
	defer UnregisterServer(server)

	// Must not error and must not publish (accept-and-drop).
	if err := PublishCustomEvent(server, "proj_123", nil); err != nil {
		t.Fatalf("PublishCustomEvent returned error: %v", err)
	}
}

// captureCustomEvent publishes a custom event against a stand-in AgentCat API
// and returns the event exactly as it reached the wire. It resets the global
// publisher so the capture URL takes effect, and drains it before decoding.
func captureCustomEvent(t *testing.T, target any, projectID string, data *CustomEventData) *Event {
	t.Helper()

	var (
		mu     sync.Mutex
		bodies [][]byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	t.Setenv("AGENTCAT_API_URL", srv.URL)
	publisher.ShutdownGlobal(context.Background()) // fresh publisher bound to the capture API
	t.Cleanup(func() { _ = publisher.ShutdownGlobal(context.Background()) })

	if err := PublishCustomEvent(target, projectID, data); err != nil {
		t.Fatalf("PublishCustomEvent returned error: %v", err)
	}

	// Drain so the event is guaranteed to have reached the capture API.
	publisher.ShutdownGlobal(context.Background())

	mu.Lock()
	defer mu.Unlock()
	var custom []*Event
	for _, body := range bodies {
		var evt Event
		if err := json.Unmarshal(body, &evt.PublishEventRequest); err != nil {
			t.Fatalf("failed to decode published event %s: %v", body, err)
		}
		if evt.GetEventType() == CustomEventType {
			custom = append(custom, &evt)
		}
	}
	if len(custom) != 1 {
		t.Fatalf("expected exactly 1 custom event at the capture API, got %d (bodies: %d)", len(custom), len(bodies))
	}
	return custom[0]
}

// captureCustomEventForTrackedServer registers a fake server in the registry
// and captures a custom event published against it.
func captureCustomEventForTrackedServer(t *testing.T, data *CustomEventData) *Event {
	t.Helper()

	server := &testServer{name: "capture-tracked"}
	instance := &AgentCatInstance{
		ProjectID: "proj_1",
		Options:   &Options{},
	}
	RegisterServer(server, instance)
	t.Cleanup(func() { UnregisterServer(server) })

	return captureCustomEvent(t, server, "proj_1", data)
}

func TestPublishCustomEventStringTargetIsVerbatim(t *testing.T) {
	evt := captureCustomEvent(t, "my-correlation-id", "proj_1", &CustomEventData{ResourceName: "r"})
	if evt.GetSessionId() != "my-correlation-id" {
		t.Errorf("sessionId = %q; string targets are used verbatim in v2", evt.GetSessionId())
	}
}

func TestPublishCustomEventSessionIDFieldWins(t *testing.T) {
	evt := captureCustomEvent(t, "ignored-string", "proj_1", &CustomEventData{SessionID: "ses_explicit"})
	if evt.GetSessionId() != "ses_explicit" {
		t.Errorf("sessionId = %q; CustomEventData.SessionID must take precedence", evt.GetSessionId())
	}
}

func TestPublishCustomEventTrackedServerHasNoSessionByDefault(t *testing.T) {
	evt := captureCustomEventForTrackedServer(t, &CustomEventData{ResourceName: "r"})
	if evt.GetSessionId() != "" {
		t.Errorf("sessionId = %q; tracked-server custom events publish without a session unless given", evt.GetSessionId())
	}
}

func TestPublishCustomEventTrackedServerWithSessionIDField(t *testing.T) {
	evt := captureCustomEventForTrackedServer(t, &CustomEventData{SessionID: "ses_from_field"})
	if evt.GetSessionId() != "ses_from_field" {
		t.Errorf("sessionId = %q; SessionID must attribute tracked-server events to the given session", evt.GetSessionId())
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
