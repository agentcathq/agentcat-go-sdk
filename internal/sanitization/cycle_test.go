package sanitization

import (
	"testing"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
)

// TestSanitizeEvent_CyclicValues verifies that self-referential customer
// payloads cannot cause unbounded recursion (a stack overflow is a fatal,
// unrecoverable runtime error).
func TestSanitizeEvent_CyclicValues(t *testing.T) {
	cyclic := map[string]any{"name": "cycle"}
	cyclic["self"] = cyclic

	cyclicSlice := []any{"x", nil}
	cyclicSlice[1] = cyclicSlice

	event := &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Parameters: map[string]any{
				"cycle": cyclic,
				"slice": cyclicSlice,
			},
			Response: map[string]any{
				"structuredContent": map[string]any{"loop": cyclic},
			},
		},
	}

	SanitizeEvent(event)

	inner, ok := event.Parameters["cycle"].(map[string]any)
	if !ok {
		t.Fatalf("cycle field lost: %T", event.Parameters["cycle"])
	}
	if inner["self"] != circularPlaceholder {
		t.Errorf("expected circular placeholder, got %v", inner["self"])
	}
}

// TestSanitizeEvent_DeepNestingBounded verifies the depth cap on extremely
// deep (non-cyclic) values.
func TestSanitizeEvent_DeepNestingBounded(t *testing.T) {
	deep := map[string]any{"leaf": "value"}
	for range 500 {
		deep = map[string]any{"next": deep}
	}

	event := &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Parameters: map[string]any{"deep": deep},
		},
	}

	SanitizeEvent(event)

	// Reaching here without a stack overflow is the assertion.
	if event.Parameters["deep"] == nil {
		t.Error("deep field dropped entirely")
	}
}
