package redaction

import (
	"errors"
	"testing"
	"time"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
)

func baseEvent() *core.Event {
	ts := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	req := agentcatapi.PublishEventRequest{
		Id:           core.Ptr("evt_original"),
		ProjectId:    "proj_789",
		EventType:    core.Ptr("mcp:tools/call"),
		Timestamp:    &ts,
		ResourceName: core.Ptr("add_todo"),
		UserIntent:   core.Ptr("sensitive intent"),
		Parameters:   map[string]any{"text": "sensitive text"},
		Response:     map[string]any{"content": "sensitive response"},
	}
	req.SetSessionId("ses_123")
	return &core.Event{PublishEventRequest: req}
}

func TestApplyEventRedaction_RewriteInPlace(t *testing.T) {
	event := baseEvent()
	observer := event // e.g. a capture holding the pointer before processing

	kept, err := ApplyEventRedaction(event, func(e *core.Event) (*core.Event, error) {
		modified := *e
		modified.Parameters = map[string]any{"text": "[SCRUBBED]"}
		return &modified, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !kept {
		t.Fatal("kept = false, want true")
	}
	if got := event.Parameters["text"]; got != "[SCRUBBED]" {
		t.Errorf("Parameters[text] = %v, want [SCRUBBED]", got)
	}
	if got := event.Response["content"]; got != "sensitive response" {
		t.Errorf("Response[content] = %v, want unchanged", got)
	}
	// The original pointer must observe the rewrite
	if got := observer.Parameters["text"]; got != "[SCRUBBED]" {
		t.Errorf("observer pointer did not see the rewrite: %v", got)
	}
}

func TestApplyEventRedaction_NilResultDropsEvent(t *testing.T) {
	event := baseEvent()

	kept, err := ApplyEventRedaction(event, func(e *core.Event) (*core.Event, error) {
		return nil, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kept {
		t.Fatal("kept = true, want false (drop)")
	}
	// The event is left untouched on drop
	if got := event.Parameters["text"]; got != "sensitive text" {
		t.Errorf("dropped event was mutated: %v", got)
	}
}

func TestApplyEventRedaction_ErrorDropsEvent(t *testing.T) {
	event := baseEvent()

	kept, err := ApplyEventRedaction(event, func(e *core.Event) (*core.Event, error) {
		return nil, errors.New("hook failure")
	})

	if err == nil || err.Error() != "hook failure" {
		t.Fatalf("err = %v, want hook failure", err)
	}
	if kept {
		t.Fatal("kept = true, want false")
	}
}

func TestApplyEventRedaction_PanicBecomesError(t *testing.T) {
	event := baseEvent()

	kept, err := ApplyEventRedaction(event, func(e *core.Event) (*core.Event, error) {
		panic("hook exploded")
	})

	if err == nil {
		t.Fatal("err = nil, want panic converted to error")
	}
	if kept {
		t.Fatal("kept = true, want false")
	}
}

func TestApplyEventRedaction_RestoresSystemManagedFields(t *testing.T) {
	event := baseEvent()

	kept, err := ApplyEventRedaction(event, func(e *core.Event) (*core.Event, error) {
		modified := *e
		modified.Id = core.Ptr("evt_forged")
		modified.SetSessionId("ses_forged")
		modified.ProjectId = "proj_forged"
		modified.EventType = core.Ptr("forged:event")
		modified.Timestamp = nil
		return &modified, nil
	})

	if err != nil || !kept {
		t.Fatalf("kept=%v err=%v, want kept with no error", kept, err)
	}
	if got := event.GetId(); got != "evt_original" {
		t.Errorf("Id = %q, want evt_original", got)
	}
	if got := event.GetSessionId(); got != "ses_123" {
		t.Errorf("SessionId = %q, want ses_123", got)
	}
	if event.ProjectId != "proj_789" {
		t.Errorf("ProjectId = %q, want proj_789", event.ProjectId)
	}
	if got := event.GetEventType(); got != "mcp:tools/call" {
		t.Errorf("EventType = %q, want mcp:tools/call", got)
	}
	if event.Timestamp == nil {
		t.Error("Timestamp was cleared, want restored")
	}
}

func TestApplyEventRedaction_HonorsClearedFields(t *testing.T) {
	event := baseEvent()

	kept, err := ApplyEventRedaction(event, func(e *core.Event) (*core.Event, error) {
		modified := *e
		modified.Response = nil
		modified.UserIntent = nil
		return &modified, nil
	})

	if err != nil || !kept {
		t.Fatalf("kept=%v err=%v, want kept with no error", kept, err)
	}
	if event.Response != nil {
		t.Errorf("Response = %v, want nil (cleared by hook)", event.Response)
	}
	if event.UserIntent != nil {
		t.Errorf("UserIntent = %v, want nil (cleared by hook)", event.UserIntent)
	}
	if got := event.Parameters["text"]; got != "sensitive text" {
		t.Errorf("Parameters[text] = %v, want unchanged", got)
	}
}

func TestApplyEventRedaction_RestoresSystemManagedFieldsOnInPlaceMutation(t *testing.T) {
	event := baseEvent()

	kept, err := ApplyEventRedaction(event, func(e *core.Event) (*core.Event, error) {
		// Forge system fields on the same pointer instead of a copy
		e.ProjectId = "proj_forged"
		e.SetSessionId("ses_forged")
		return e, nil
	})

	if err != nil || !kept {
		t.Fatalf("kept=%v err=%v, want kept with no error", kept, err)
	}
	if event.ProjectId != "proj_789" {
		t.Errorf("ProjectId = %q, want proj_789 (restored)", event.ProjectId)
	}
	if got := event.GetSessionId(); got != "ses_123" {
		t.Errorf("SessionId = %q, want ses_123 (restored)", got)
	}
}

func TestApplyEventRedaction_SamePointerReturnIsFine(t *testing.T) {
	event := baseEvent()

	kept, err := ApplyEventRedaction(event, func(e *core.Event) (*core.Event, error) {
		e.Parameters = map[string]any{"text": "[MUTATED]"}
		return e, nil
	})

	if err != nil || !kept {
		t.Fatalf("kept=%v err=%v, want kept with no error", kept, err)
	}
	if got := event.Parameters["text"]; got != "[MUTATED]" {
		t.Errorf("Parameters[text] = %v, want [MUTATED]", got)
	}
}
