// Package truncation applies layered size limits to events before they are
// sent to the AgentCat API, mirroring the TypeScript SDK's truncation module:
//
//  1. Field-level string limits (userIntent, resourceName, server/client
//     metadata, error.message) and error frame limiting
//  2. Response content text limits (32KB per text block)
//  3. Recursive normalization on user-controlled fields (depth 10,
//     breadth 100, 32KB per string)
//  4. Size-targeted truncation to a 100KB serialized event budget
//     (progressive depth reduction, then largest-string truncation)
package truncation

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"sort"

	"go.agentcat.com/sdk/internal/core"
)

const (
	// MaxDepth is the maximum nesting depth kept during normalization.
	MaxDepth = 10

	// MaxBreadth is the maximum number of entries kept per map or slice.
	MaxBreadth = 100

	// MaxStringLength is the per-string cap (32KB) during normalization.
	MaxStringLength = 32768

	// MaxEventBytes is the total serialized event budget (100KB).
	MaxEventBytes = 102400

	maxUserIntentLength   = 2048
	maxErrorMessageLength = 2048
	maxResourceNameLength = 256
	maxMetadataLength     = 256
	maxStackFrames        = 50
	maxContentTextLength  = 32768

	truncationSuffix = "..."
)

// TruncateEvent applies layered truncation to an event in place.
func TruncateEvent(event *core.Event) {
	if event == nil {
		return
	}

	// Layer 1: field-level string limits.
	event.UserIntent = truncateStringPtr(event.UserIntent, maxUserIntentLength)
	event.ResourceName = truncateStringPtr(event.ResourceName, maxResourceNameLength)
	event.ServerName = truncateStringPtr(event.ServerName, maxMetadataLength)
	event.ServerVersion = truncateStringPtr(event.ServerVersion, maxMetadataLength)
	event.ClientName = truncateStringPtr(event.ClientName, maxMetadataLength)
	event.ClientVersion = truncateStringPtr(event.ClientVersion, maxMetadataLength)

	// Error field limits.
	if event.Error != nil {
		event.Error = truncateErrorFields(event.Error)
	}

	// Response content text limits.
	if event.Response != nil {
		event.Response = truncateResponseContent(event.Response)
	}

	// Layer 2: recursive normalization on user-controlled fields.
	event.Parameters = normalizeMapField(event.Parameters, MaxDepth)
	event.Response = normalizeMapField(event.Response, MaxDepth)
	event.IdentifyData = normalizeMapField(event.IdentifyData, MaxDepth)
	event.Error = normalizeMapField(event.Error, MaxDepth)

	// Layer 3: size-targeted truncation.
	truncateToSize(event)
}

// --- Normalization ---

// Normalize recursively normalizes a value: strings are truncated to
// maxStringLength, maps/slices are limited to maxBreadth entries and depth
// levels, non-JSON-serializable values are replaced with placeholders, and
// circular references are detected.
func Normalize(input any, depth int) any {
	memo := make(map[uintptr]bool)
	return visit(input, depth, memo, false)
}

// visit normalizes a single value. converted guards against infinite loops
// when unknown types are converted via JSON round-trip.
func visit(value any, remainingDepth int, memo map[uintptr]bool, converted bool) any {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	case bool:
		return v

	case string:
		return truncateString(v, MaxStringLength)

	case float64:
		if math.IsNaN(v) {
			return "[NaN]"
		}
		if math.IsInf(v, 1) {
			return "[Infinity]"
		}
		if math.IsInf(v, -1) {
			return "[-Infinity]"
		}
		return v

	case float32:
		return visit(float64(v), remainingDepth, memo, converted)

	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return v

	case map[string]any:
		ptr := reflect.ValueOf(v).Pointer()
		if memo[ptr] {
			return "[Circular ~]"
		}
		if remainingDepth <= 0 {
			return "[Object]"
		}
		memo[ptr] = true
		result := visitMap(v, remainingDepth-1, memo)
		delete(memo, ptr)
		return result

	case []any:
		if len(v) > 0 {
			ptr := reflect.ValueOf(v).Pointer()
			if memo[ptr] {
				return "[Circular ~]"
			}
			if remainingDepth <= 0 {
				return "[Array]"
			}
			memo[ptr] = true
			result := visitSlice(v, remainingDepth-1, memo)
			delete(memo, ptr)
			return result
		}
		if remainingDepth <= 0 {
			return "[Array]"
		}
		return v

	default:
		// Unknown type (struct, typed map/slice, etc). Try a JSON round-trip
		// once so nested strings and collections are normalized too.
		if !converted {
			if roundTripped, ok := core.JSONRoundTrip(v); ok {
				return visit(roundTripped, remainingDepth, memo, true)
			}
		}
		// Not JSON-serializable: coerce to a (truncated) string placeholder.
		return truncateString(fmt.Sprintf("%v", v), MaxStringLength)
	}
}

func visitMap(m map[string]any, remainingDepth int, memo map[uintptr]bool) map[string]any {
	result := make(map[string]any, len(m))

	// At or below the breadth limit no trimming occurs and map output order
	// is irrelevant, so skip the sort.
	if len(m) <= MaxBreadth {
		for k, v := range m {
			result[k] = visit(v, remainingDepth, memo, false)
		}
		return result
	}

	// Over the limit: sort keys so the kept subset is deterministic.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	count := 0
	for _, k := range keys {
		if count >= MaxBreadth {
			result["..."] = "[MaxProperties ~]"
			break
		}
		result[k] = visit(m[k], remainingDepth, memo, false)
		count++
	}
	return result
}

func visitSlice(s []any, remainingDepth int, memo map[uintptr]bool) []any {
	result := make([]any, 0, len(s))
	for i, item := range s {
		if i >= MaxBreadth {
			result = append(result, "[MaxProperties ~]")
			break
		}
		result = append(result, visit(item, remainingDepth, memo, false))
	}
	return result
}

// normalizeMapField normalizes a map field, keeping the map type expected by
// the wire event.
func normalizeMapField(m map[string]any, depth int) map[string]any {
	if m == nil {
		return nil
	}
	if normalized, ok := Normalize(m, depth).(map[string]any); ok {
		return normalized
	}
	return m
}

// --- Field-level truncation helpers ---

func truncateString(s string, maxLength int) string {
	if len(s) <= maxLength {
		return s
	}
	return s[:maxLength] + truncationSuffix
}

func truncateStringPtr(s *string, maxLength int) *string {
	if s == nil || len(*s) <= maxLength {
		return s
	}
	truncated := truncateString(*s, maxLength)
	return &truncated
}

// truncateErrorFields limits error.message length and the number of stack
// frames (keeping the first and last 25 when over the limit).
func truncateErrorFields(errMap map[string]any) map[string]any {
	result := make(map[string]any, len(errMap))
	maps.Copy(result, errMap)

	if msg, ok := result["message"].(string); ok {
		result["message"] = truncateString(msg, maxErrorMessageLength)
	}

	if frames := toAnySlice(result["frames"]); len(frames) > maxStackFrames {
		half := maxStackFrames / 2
		truncated := make([]any, 0, maxStackFrames)
		truncated = append(truncated, frames[:half]...)
		truncated = append(truncated, frames[len(frames)-half:]...)
		result["frames"] = truncated
	}

	return result
}

// toAnySlice converts frames stored as []any or []map[string]any to []any.
func toAnySlice(v any) []any {
	switch frames := v.(type) {
	case []any:
		return frames
	case []map[string]any:
		result := make([]any, len(frames))
		for i, f := range frames {
			result[i] = f
		}
		return result
	default:
		return nil
	}
}

// truncateResponseContent caps text content blocks at maxContentTextLength.
func truncateResponseContent(response map[string]any) map[string]any {
	content, ok := response["content"].([]any)
	if !ok {
		return response
	}

	result := make(map[string]any, len(response))
	maps.Copy(result, response)

	newContent := make([]any, len(content))
	for i, block := range content {
		blockMap, ok := block.(map[string]any)
		if !ok {
			newContent[i] = block
			continue
		}
		text, isText := blockMap["text"].(string)
		if blockMap["type"] == "text" && isText && len(text) > maxContentTextLength {
			newBlock := make(map[string]any, len(blockMap))
			maps.Copy(newBlock, blockMap)
			newBlock["text"] = truncateString(text, maxContentTextLength)
			newContent[i] = newBlock
		} else {
			newContent[i] = block
		}
	}
	result["content"] = newContent
	return result
}

// --- Size-targeted truncation ---

func eventByteSize(event *core.Event) int {
	data, err := json.Marshal(event.PublishEventRequest)
	if err != nil {
		return 0
	}
	return len(data)
}

// truncateToSize ensures the serialized event fits within MaxEventBytes by
// progressively reducing normalization depth on the user-controlled fields,
// then truncating the largest string values as a last resort.
func truncateToSize(event *core.Event) {
	if eventByteSize(event) <= MaxEventBytes {
		return
	}

	// Keep the original (already normalized) fields so each depth pass
	// reduces from the same starting point, like the TS implementation.
	origParams := event.Parameters
	origResponse := event.Response
	origIdentify := event.IdentifyData
	origError := event.Error

	for depth := MaxDepth - 1; depth >= 1; depth-- {
		event.Parameters = normalizeMapField(origParams, depth)
		event.Response = normalizeMapField(origResponse, depth)
		event.IdentifyData = normalizeMapField(origIdentify, depth)
		event.Error = normalizeMapField(origError, depth)

		if eventByteSize(event) <= MaxEventBytes {
			return
		}
	}

	// Last resort: truncate the largest string values across the
	// user-controlled fields (already reduced to depth 1 above).
	truncateLargestFields(event)
}

// truncateLargestFields finds the largest string values across the event's
// user-controlled fields and truncates them until the event fits within
// MaxEventBytes (or no further reduction is possible).
func truncateLargestFields(event *core.Event) {
	fields := map[string]map[string]any{
		"parameters":   event.Parameters,
		"response":     event.Response,
		"identifyData": event.IdentifyData,
		"error":        event.Error,
	}

	for range 10 {
		currentSize := eventByteSize(event)
		if currentSize <= MaxEventBytes {
			return
		}
		excess := currentSize - MaxEventBytes

		// Collect all string values >100 chars with their paths, largest first.
		var paths []stringPath
		for fieldName, field := range fields {
			if field == nil {
				continue
			}
			collectStringPaths(field, []any{fieldName}, &paths)
		}
		if len(paths) == 0 {
			return
		}
		sort.Slice(paths, func(i, j int) bool { return paths[i].length > paths[j].length })

		// Distribute the reduction across the largest strings.
		remaining := excess + 200 // buffer for JSON overhead from added suffixes
		truncated := false

		for _, sp := range paths {
			if remaining <= 0 {
				break
			}
			reduction := min(sp.length/2, remaining)
			if reduction < 10 {
				continue
			}
			newLength := sp.length - reduction
			current, ok := getNestedString(fields, sp.path)
			if !ok {
				continue
			}
			setNestedValue(fields, sp.path, current[:newLength]+truncationSuffix)
			remaining -= reduction
			truncated = true
		}

		if !truncated {
			return
		}
	}
}

type stringPath struct {
	path   []any // first element is the field name (string); rest are string keys or int indices
	length int
}

func collectStringPaths(value any, currentPath []any, results *[]stringPath) {
	switch v := value.(type) {
	case string:
		if len(v) > 100 {
			path := make([]any, len(currentPath))
			copy(path, currentPath)
			*results = append(*results, stringPath{path: path, length: len(v)})
		}
	case []any:
		for i, item := range v {
			collectStringPaths(item, append(currentPath, i), results)
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			collectStringPaths(v[k], append(currentPath, k), results)
		}
	}
}

func getNestedString(fields map[string]map[string]any, path []any) (string, bool) {
	var current any = fields[path[0].(string)]
	for _, seg := range path[1:] {
		switch key := seg.(type) {
		case string:
			m, ok := current.(map[string]any)
			if !ok {
				return "", false
			}
			current = m[key]
		case int:
			s, ok := current.([]any)
			if !ok || key >= len(s) {
				return "", false
			}
			current = s[key]
		}
	}
	str, ok := current.(string)
	return str, ok
}

func setNestedValue(fields map[string]map[string]any, path []any, value any) {
	if len(path) < 2 {
		return
	}
	var current any = fields[path[0].(string)]
	for _, seg := range path[1 : len(path)-1] {
		switch key := seg.(type) {
		case string:
			m, ok := current.(map[string]any)
			if !ok {
				return
			}
			current = m[key]
		case int:
			s, ok := current.([]any)
			if !ok || key >= len(s) {
				return
			}
			current = s[key]
		}
	}
	last := path[len(path)-1]
	switch key := last.(type) {
	case string:
		if m, ok := current.(map[string]any); ok {
			m[key] = value
		}
	case int:
		if s, ok := current.([]any); ok && key < len(s) {
			s[key] = value
		}
	}
}
