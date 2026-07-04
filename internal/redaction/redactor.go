package redaction

import (
	"go.agentcat.com/sdk/internal/core"
)

const redactionErrorPlaceholder = "[REDACTION_ERROR]"

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

// redactMap recursively processes a map, creating a new map with redacted string values
func redactMap(m map[string]any, redactFn core.RedactFunc) map[string]any {
	if m == nil {
		return nil
	}

	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = redactValue(v, redactFn)
	}
	return result
}

// redactValue recursively processes a value based on its type
func redactValue(v any, redactFn core.RedactFunc) any {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case string:
		// Apply redaction function with panic recovery
		return safeRedact(val, redactFn)

	case map[string]any:
		// Recursively redact nested maps
		return redactMap(val, redactFn)

	case []any:
		// Recursively redact slices
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = redactValue(item, redactFn)
		}
		return result

	default:
		// For other types (numbers, bools, etc.), return as-is
		return v
	}
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
