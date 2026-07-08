package redaction

import (
	"testing"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
)

// TestRedactEvent_CyclicParameters verifies that self-referential customer
// payloads cannot cause unbounded recursion (a stack overflow is a fatal,
// unrecoverable runtime error).
func TestRedactEvent_CyclicParameters(t *testing.T) {
	cyclic := map[string]any{"name": "cycle"}
	cyclic["self"] = cyclic

	cyclicSlice := []any{"x", nil}
	cyclicSlice[1] = cyclicSlice

	event := &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Parameters: map[string]any{
				"cycle": cyclic,
				"slice": cyclicSlice,
				"plain": "secret",
			},
		},
	}

	if err := RedactEvent(event, func(s string) string { return "R" }); err != nil {
		t.Fatalf("RedactEvent returned error: %v", err)
	}

	if event.Parameters["plain"] != "R" {
		t.Errorf("plain string not redacted: %v", event.Parameters["plain"])
	}
	inner, ok := event.Parameters["cycle"].(map[string]any)
	if !ok {
		t.Fatalf("cycle field lost: %T", event.Parameters["cycle"])
	}
	if inner["self"] != circularPlaceholder {
		t.Errorf("expected circular placeholder, got %v", inner["self"])
	}
}

// TestRedactEvent_DeepNestingBounded verifies the depth cap on extremely deep
// (non-cyclic) values.
func TestRedactEvent_DeepNestingBounded(t *testing.T) {
	deep := map[string]any{"leaf": "secret"}
	for range 500 {
		deep = map[string]any{"next": deep}
	}

	event := &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Parameters: map[string]any{"deep": deep},
		},
	}

	if err := RedactEvent(event, func(s string) string { return "R" }); err != nil {
		t.Fatalf("RedactEvent returned error: %v", err)
	}
	// Reaching here without a stack overflow is the assertion; spot-check the
	// value survived in some placeholder-or-map form.
	if event.Parameters["deep"] == nil {
		t.Error("deep field dropped entirely")
	}
}
