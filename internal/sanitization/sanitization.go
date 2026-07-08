// Package sanitization removes binary/non-text payloads from events before
// they are sent to the AgentCat API. It mirrors the TypeScript SDK's
// sanitization module: non-text content blocks in tool responses are replaced
// with placeholder text blocks, and large base64-looking strings anywhere in
// parameters or structured content are replaced with a placeholder.
package sanitization

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"

	"go.agentcat.com/sdk/internal/core"
)

const (
	// sizeGate is the minimum string length (10KB) before the base64 pattern
	// is tested, to avoid regex work on small strings.
	sizeGate = 10240

	// maxSanitizeDepth bounds recursion on pathologically deep values.
	// Recursion must be bounded here: sanitization runs before truncation's
	// cycle-safe normalization, and a stack overflow is a fatal,
	// unrecoverable runtime error.
	maxSanitizeDepth = 100

	depthLimitPlaceholder = "[MAX_DEPTH_EXCEEDED]"
	circularPlaceholder   = "[Circular ~]"

	imageRedactedText          = "[image content redacted - not supported by AgentCat]"
	audioRedactedText          = "[audio content redacted - not supported by AgentCat]"
	binaryResourceRedactedText = "[binary resource content redacted - not supported by AgentCat]"
	binaryDataRedactedText     = "[binary data redacted - not supported by AgentCat]"
)

var base64Pattern = regexp.MustCompile(`^[A-Za-z0-9+/\n\r]+=*$`)

// SanitizeEvent sanitizes an event in place, redacting non-text content
// blocks from the response and large base64-encoded strings from parameters.
// It never modifies the maps it was given; sanitized fields are replaced with
// new maps.
func SanitizeEvent(event *core.Event) {
	if event == nil {
		return
	}

	if event.Response != nil {
		event.Response = sanitizeResponse(event.Response)
	}

	if event.Parameters != nil {
		if m, ok := sanitizeValue(event.Parameters).(map[string]any); ok {
			event.Parameters = m
		}
	}
}

// sanitizeResponse sanitizes response content blocks by replacing non-text
// content types with informative redaction messages, and scans structured
// content for large base64 strings.
func sanitizeResponse(response map[string]any) map[string]any {
	result := make(map[string]any, len(response))
	maps.Copy(result, response)

	if content, ok := result["content"].([]any); ok {
		sanitized := make([]any, len(content))
		for i, block := range content {
			sanitized[i] = sanitizeContentBlock(block)
		}
		result["content"] = sanitized
	}

	if structured, ok := result["structuredContent"]; ok && structured != nil {
		if _, isMap := structured.(map[string]any); isMap {
			result["structuredContent"] = sanitizeValue(structured)
		}
	}

	return result
}

// sanitizeContentBlock sanitizes a single content block based on its type
// discriminator. Text and resource_link blocks pass through unchanged.
func sanitizeContentBlock(block any) any {
	blockMap, ok := block.(map[string]any)
	if !ok {
		return block
	}

	blockType, _ := blockMap["type"].(string)
	switch blockType {
	case "text":
		return block

	case "image":
		return textBlock(imageRedactedText)

	case "audio":
		return textBlock(audioRedactedText)

	case "resource":
		return sanitizeResourceBlock(blockMap)

	case "resource_link":
		return block

	default:
		return textBlock(fmt.Sprintf("[unsupported content type %q redacted - not supported by AgentCat]", blockType))
	}
}

// sanitizeResourceBlock sanitizes an embedded resource content block.
// Blob resource contents (with a "blob" field) are redacted; text resource
// contents pass through.
func sanitizeResourceBlock(block map[string]any) any {
	if resource, ok := block["resource"].(map[string]any); ok {
		if _, hasBlob := resource["blob"]; hasBlob {
			return textBlock(binaryResourceRedactedText)
		}
	}
	return block
}

// sanitizeValue recursively scans a value for large base64-encoded strings and
// replaces them with a placeholder. Maps and slices are copied, not mutated.
// Self-referential values and excessive depth are replaced with placeholders
// so recursion is always bounded.
func sanitizeValue(v any) any {
	return sanitizeValueBounded(v, make(map[uintptr]bool), maxSanitizeDepth)
}

func sanitizeValueBounded(v any, memo map[uintptr]bool, depth int) any {
	switch val := v.(type) {
	case string:
		if len(val) >= sizeGate && base64Pattern.MatchString(val) {
			return binaryDataRedactedText
		}
		return val

	case map[string]any:
		ptr := reflect.ValueOf(val).Pointer()
		if memo[ptr] {
			return circularPlaceholder
		}
		if depth <= 0 {
			return depthLimitPlaceholder
		}
		memo[ptr] = true
		result := make(map[string]any, len(val))
		for k, item := range val {
			result[k] = sanitizeValueBounded(item, memo, depth-1)
		}
		delete(memo, ptr)
		return result

	case []any:
		if len(val) == 0 {
			return val
		}
		ptr := reflect.ValueOf(val).Pointer()
		if memo[ptr] {
			return circularPlaceholder
		}
		if depth <= 0 {
			return depthLimitPlaceholder
		}
		memo[ptr] = true
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = sanitizeValueBounded(item, memo, depth-1)
		}
		delete(memo, ptr)
		return result

	default:
		return v
	}
}

func textBlock(text string) map[string]any {
	return map[string]any{
		"type": "text",
		"text": text,
	}
}
