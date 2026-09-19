// Package tokens estimates the token counts a tool call costs the model, for
// the input_tokens / output_tokens fields on mcp:tools/call events.
//
// One divisor, pinned byte-for-byte across the TypeScript, Python and Go
// SDKs: ceil(utf8Bytes / 3.5). Measured on production MCP responses, the
// inner text runs 3.71 bytes per token on OpenAI tokenizers and about 3.15 on
// Claude's; 3.5 splits the difference. The input side counts the raw
// arguments the model emitted; the output side counts the content-block text
// the harness feeds back. Nothing else counts. See the TypeScript repo's
// docs/superpowers/specs/2026-09-19-sdk-token-estimates-design.md.
package tokens

import (
	"bytes"
	"encoding/json"
	"math"
)

// BytesPerToken is the one tunable, named TOKEN_ESTIMATE_BYTES_PER_TOKEN in
// the TypeScript and Python SDKs.
const BytesPerToken = 3.5

// maxTokenCount is the server's MAX_TOKEN_COUNT: the columns are int32.
const maxTokenCount = math.MaxInt32

// Estimate returns ceil(byteCount / 3.5), 0 for nothing, clamped to int32.
func Estimate(byteCount int) int32 {
	if byteCount <= 0 {
		return 0
	}
	n := math.Ceil(float64(byteCount) / BytesPerToken)
	if n > maxTokenCount {
		return maxTokenCount
	}
	return int32(n)
}

// compactJSONBytes is the UTF-8 length of the compact JSON with no HTML
// escaping: json.Marshal would replace each of <, > and & with a six-byte
// \u escape and count 44 bytes for {"html":"<a>&</a>"} where TypeScript and
// Python count 19. ok is false when the value cannot be encoded — including
// when a customer's MarshalJSON panics: this runs inside the customer's
// request, and a failure to estimate must cost at most the field, never the
// tool's response.
func compactJSONBytes(value any) (n int, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			n, ok = 0, false
		}
	}()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return 0, false
	}
	// Encode appends a newline that is not part of the document.
	return buf.Len() - 1, true
}

// InputTokens estimates the tokens the model spent emitting the call: the raw
// arguments, injected parameters included. ok is false when there is nothing
// to count. An empty (non-nil) map counts as {}.
func InputTokens(arguments map[string]any) (int32, bool) {
	if arguments == nil {
		return 0, false
	}
	n, ok := compactJSONBytes(arguments)
	if !ok {
		return 0, false
	}
	return Estimate(n), true
}

// OutputTokens estimates the tokens the model reads back: the text of the
// content blocks. Falls back to the whole response when it carries no content
// list. ok is false when there is no response at all.
func OutputTokens(response map[string]any) (int32, bool) {
	if response == nil {
		return 0, false
	}
	content, isList := response["content"].([]any)
	if !isList {
		n, ok := compactJSONBytes(response)
		if !ok {
			return 0, false
		}
		return Estimate(n), true
	}
	total := 0
	for _, block := range content {
		total += contentBlockBytes(block)
	}
	return Estimate(total), true
}

func contentBlockBytes(block any) int {
	m, ok := block.(map[string]any)
	if !ok {
		return 0
	}
	switch m["type"] {
	case "text":
		if text, ok := m["text"].(string); ok {
			return len(text)
		}
	case "resource":
		if resource, ok := m["resource"].(map[string]any); ok {
			if text, ok := resource["text"].(string); ok {
				return len(text)
			}
		}
	}
	return 0
}
