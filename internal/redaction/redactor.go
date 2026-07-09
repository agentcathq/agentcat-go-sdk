package redaction

import (
	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/walk"
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
