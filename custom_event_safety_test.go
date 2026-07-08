package agentcat

import (
	"context"
	"math"
	"testing"
	"time"

	"go.agentcat.com/sdk/internal/publisher"
)

// hostileErr has an unhashable dynamic type (slice field, value receiver);
// error capture must tolerate it.
type hostileErr struct{ parts []string }

func (e hostileErr) Error() string { return "hostile custom error" }

// TestPublishCustomEvent_HostilePayloads pushes adversarial customer payloads
// (channels, funcs, NaN/Inf, self-referential maps, unhashable error types)
// through the full custom-event publish pipeline. The invariant: no crash, no
// hang; the call returns nil and the publisher drains under its deadline.
func TestPublishCustomEvent_HostilePayloads(t *testing.T) {
	t.Setenv("AGENTCAT_API_URL", "http://localhost:0") // fail sends fast, no real API

	publisher.ShutdownGlobal(context.Background())
	defer publisher.ShutdownGlobal(context.Background())

	cyclic := map[string]any{"name": "cycle"}
	cyclic["self"] = cyclic

	err := PublishCustomEvent("hostile-session", "proj_123", &CustomEventData{
		ResourceName: "hostile-action",
		Parameters: map[string]any{
			"channel": make(chan int),
			"fn":      func() {},
			"nan":     math.NaN(),
			"inf":     math.Inf(1),
			"cycle":   cyclic,
		},
		Response: map[string]any{"loop": cyclic},
		Message:  "hostile payload test",
		IsError:  true,
		Error:    hostileErr{parts: []string{"a", "b"}},
		Properties: map[string]any{
			"cyclicProp": cyclic,
		},
	})
	if err != nil {
		t.Fatalf("PublishCustomEvent returned error: %v", err)
	}

	// Drain with a bounded deadline; a pipeline hang or crash fails here.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	publisher.ShutdownGlobal(ctx)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("shutdown took %v; pipeline appears blocked", elapsed)
	}
}
