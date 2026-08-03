package redaction

import (
	"fmt"

	"go.agentcat.com/sdk/v2/internal/core"
	"go.agentcat.com/sdk/v2/internal/walk"
)

const (
	redactionErrorPlaceholder = "[REDACTION_ERROR]"

	// Placeholders produced by the shared bounded walk. Recursion must be
	// bounded here: redaction runs before truncation's cycle-safe
	// normalization, and a stack overflow is a fatal, unrecoverable runtime
	// error.
	depthLimitPlaceholder = walk.DepthLimitPlaceholder
	circularPlaceholder   = walk.CircularPlaceholder
)

// RedactEvent applies the redaction function to all string values in the event's
// Parameters, Response, UserIntent, and Error fields. It recursively descends
// into nested maps and slices. If the user-provided redaction function panics
// on a particular string, that string is replaced with [REDACTION_ERROR] (via
// safeRedact) rather than crashing the publisher.
//
// This function creates a deep copy of the maps to avoid mutating the original event.
func RedactEvent(event *core.Event, redactFn core.RedactFunc) error {
	if event == nil || redactFn == nil {
		return nil
	}

	if event.Parameters != nil {
		event.Parameters = redactMap(event.Parameters, redactFn)
	}

	// Redact Response map
	if event.Response != nil {
		event.Response = redactMap(event.Response, redactFn)
	}

	// Redact UserIntent string
	if event.UserIntent != nil && *event.UserIntent != "" {
		redacted := safeRedact(*event.UserIntent, redactFn)
		event.UserIntent = &redacted
	}

	// Redact Error map (message and stack traces can contain sensitive data)
	if event.Error != nil {
		event.Error = redactMap(event.Error, redactFn)
	}

	return nil
}

// redactMap recursively processes a map, creating a new map with redacted
// string values. Self-referential values and excessive depth are replaced
// with placeholders so recursion is always bounded.
func redactMap(m map[string]any, redactFn core.RedactFunc) map[string]any {
	if m == nil {
		return nil
	}
	result, _ := redactValue(m, redactFn).(map[string]any)
	return result
}

// redactValue recursively redacts all string values in v via the shared
// bounded deep-walk (cycle- and depth-guarded).
func redactValue(v any, redactFn core.RedactFunc) any {
	return walk.Bounded(v, func(s string) any {
		return safeRedact(s, redactFn)
	})
}

// ApplyEventRedaction applies the customer's event-level redaction hook to an
// event, in place. The hook receives the full event and may return a modified
// event, or nil to drop the event entirely.
//
// The event object is rewritten rather than replaced: publisher workers and
// their observers hold the same pointer across processing, so a hook that
// returns a new event has its result copied into the original allocation,
// which also ensures fields the hook cleared stay cleared. System-managed
// fields are restored from the original. A panic inside the hook is converted
// to an error (the caller drops the event, mirroring hook errors).
//
// Returns kept=false when the hook dropped the event (nil result), and a
// non-nil error when the hook failed.
func ApplyEventRedaction(event *core.Event, redactEventFn core.RedactEventFunc) (kept bool, err error) {
	if event == nil || redactEventFn == nil {
		return true, nil
	}

	// Snapshot system-managed fields before the hook runs — they are not
	// consumer-settable, even by a hook that mutates the event in place
	id := event.Id
	sessionID := event.SessionId
	projectID := event.ProjectId
	eventType := event.EventType
	timestamp := event.Timestamp

	var result *core.Event
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("event redaction hook panicked: %v", r)
			}
		}()
		result, err = redactEventFn(event)
	}()
	if err != nil {
		return false, err
	}
	if result == nil {
		return false, nil
	}

	if result != event {
		// Copy the hook's result into the original allocation so pointer
		// holders see the post-hook event, and cleared fields stay cleared
		*event = *result
	}

	event.Id = id
	event.SessionId = sessionID
	event.ProjectId = projectID
	event.EventType = eventType
	event.Timestamp = timestamp

	return true, nil
}

// safeRedact applies the redaction function with panic recovery
func safeRedact(s string, redactFn core.RedactFunc) string {
	var result string
	var panicked bool

	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		result = redactFn(s)
	}()

	if panicked {
		return redactionErrorPlaceholder
	}

	return result
}
