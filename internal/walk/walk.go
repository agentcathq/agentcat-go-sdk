// Package walk provides a shared bounded deep-walk over JSON-shaped values
// (map[string]any / []any trees). Self-referential values and excessive depth
// are replaced with placeholders so recursion is always bounded: the walk runs
// before truncation's cycle-safe normalization, and a stack overflow is a
// fatal, unrecoverable runtime error.
package walk

import "reflect"

const (
	// MaxDepth bounds recursion on pathologically deep values.
	MaxDepth = 100

	// DepthLimitPlaceholder replaces values nested deeper than MaxDepth.
	DepthLimitPlaceholder = "[MAX_DEPTH_EXCEEDED]"

	// CircularPlaceholder replaces self-referential values.
	CircularPlaceholder = "[Circular ~]"
)

// Bounded deep-walks v, applying transform to every string leaf and copying
// maps and slices (the input is never mutated). Non-string scalars pass
// through unchanged.
func Bounded(v any, transform func(string) any) any {
	return bounded(v, transform, make(map[uintptr]bool), MaxDepth)
}

func bounded(v any, transform func(string) any, memo map[uintptr]bool, depth int) any {
	switch val := v.(type) {
	case string:
		return transform(val)

	case map[string]any:
		ptr := reflect.ValueOf(val).Pointer()
		if memo[ptr] {
			return CircularPlaceholder
		}
		if depth <= 0 {
			return DepthLimitPlaceholder
		}
		memo[ptr] = true
		result := make(map[string]any, len(val))
		for k, item := range val {
			result[k] = bounded(item, transform, memo, depth-1)
		}
		delete(memo, ptr)
		return result

	case []any:
		if len(val) == 0 {
			return val
		}
		ptr := reflect.ValueOf(val).Pointer()
		if memo[ptr] {
			return CircularPlaceholder
		}
		if depth <= 0 {
			return DepthLimitPlaceholder
		}
		memo[ptr] = true
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = bounded(item, transform, memo, depth-1)
		}
		delete(memo, ptr)
		return result

	default:
		// Other types (numbers, bools, nil, etc.) pass through as-is.
		return v
	}
}
