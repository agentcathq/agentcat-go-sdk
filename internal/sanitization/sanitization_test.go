package sanitization

import (
	"strings"
	"testing"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
)

func makeEvent(params, response map[string]any) *core.Event {
	return &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Parameters: params,
			Response:   response,
		},
	}
}

func contentBlocks(t *testing.T, evt *core.Event) []any {
	t.Helper()
	content, ok := evt.Response["content"].([]any)
	if !ok {
		t.Fatalf("response content is not []any: %T", evt.Response["content"])
	}
	return content
}

func TestSanitizeEvent_NilEvent(t *testing.T) {
	SanitizeEvent(nil) // must not panic
}

func TestSanitizeEvent_ContentBlocks(t *testing.T) {
	tests := []struct {
		name        string
		block       map[string]any
		wantText    string // expected replacement text; empty means block passes through
		passThrough bool
	}{
		{
			name:        "text block passes through",
			block:       map[string]any{"type": "text", "text": "hello"},
			passThrough: true,
		},
		{
			name:     "image block redacted",
			block:    map[string]any{"type": "image", "data": "aGVsbG8=", "mimeType": "image/png"},
			wantText: imageRedactedText,
		},
		{
			name:     "audio block redacted",
			block:    map[string]any{"type": "audio", "data": "aGVsbG8=", "mimeType": "audio/wav"},
			wantText: audioRedactedText,
		},
		{
			name: "blob resource redacted",
			block: map[string]any{
				"type":     "resource",
				"resource": map[string]any{"uri": "file:///x.bin", "blob": "aGVsbG8="},
			},
			wantText: binaryResourceRedactedText,
		},
		{
			name: "text resource passes through",
			block: map[string]any{
				"type":     "resource",
				"resource": map[string]any{"uri": "file:///x.txt", "text": "hello"},
			},
			passThrough: true,
		},
		{
			name:        "resource_link passes through",
			block:       map[string]any{"type": "resource_link", "uri": "file:///x"},
			passThrough: true,
		},
		{
			name:     "unknown type redacted",
			block:    map[string]any{"type": "video", "data": "..."},
			wantText: `[unsupported content type "video" redacted - not supported by AgentCat]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := makeEvent(nil, map[string]any{"content": []any{tt.block}})
			SanitizeEvent(evt)

			got := contentBlocks(t, evt)[0].(map[string]any)
			if tt.passThrough {
				if got["type"] != tt.block["type"] {
					t.Errorf("block type changed: got %v, want %v", got["type"], tt.block["type"])
				}
				return
			}
			if got["type"] != "text" {
				t.Errorf("replacement type = %v, want text", got["type"])
			}
			if got["text"] != tt.wantText {
				t.Errorf("replacement text = %q, want %q", got["text"], tt.wantText)
			}
		})
	}
}

func TestSanitizeEvent_Base64Parameters(t *testing.T) {
	bigBase64 := strings.Repeat("ABCDefgh0123+/", 1000) // 14000 chars, base64 alphabet

	smallBase64 := "aGVsbG8gd29ybGQ="
	bigText := strings.Repeat("hello world! ", 1000) // >10KB but not base64

	evt := makeEvent(map[string]any{
		"big":    bigBase64,
		"small":  smallBase64,
		"text":   bigText,
		"nested": map[string]any{"inner": bigBase64},
		"list":   []any{bigBase64, "ok"},
		"number": float64(42),
	}, nil)

	SanitizeEvent(evt)

	if evt.Parameters["big"] != binaryDataRedactedText {
		t.Error("large base64 string was not redacted")
	}
	if evt.Parameters["small"] != smallBase64 {
		t.Error("small base64 string should not be redacted")
	}
	if evt.Parameters["text"] != bigText {
		t.Error("large non-base64 string should not be redacted")
	}
	nested := evt.Parameters["nested"].(map[string]any)
	if nested["inner"] != binaryDataRedactedText {
		t.Error("nested base64 string was not redacted")
	}
	list := evt.Parameters["list"].([]any)
	if list[0] != binaryDataRedactedText || list[1] != "ok" {
		t.Errorf("slice sanitization incorrect: %v", list)
	}
	if evt.Parameters["number"] != float64(42) {
		t.Error("non-string value changed")
	}
}

func TestSanitizeEvent_StructuredContent(t *testing.T) {
	big := strings.Repeat("ABCDefgh0123+/", 1000)
	evt := makeEvent(nil, map[string]any{
		"structuredContent": map[string]any{"data": big},
	})

	SanitizeEvent(evt)

	sc := evt.Response["structuredContent"].(map[string]any)
	if sc["data"] != binaryDataRedactedText {
		t.Error("structuredContent base64 string was not redacted")
	}
}

func TestSanitizeEvent_DoesNotMutateOriginalMaps(t *testing.T) {
	original := map[string]any{
		"content": []any{map[string]any{"type": "image", "data": "x"}},
	}
	evt := makeEvent(nil, original)

	SanitizeEvent(evt)

	if block := original["content"].([]any)[0].(map[string]any); block["type"] != "image" {
		t.Error("original response map was mutated")
	}
}

func TestSanitizeEvent_NonMapContentBlockPassesThrough(t *testing.T) {
	evt := makeEvent(nil, map[string]any{"content": []any{"just a string"}})
	SanitizeEvent(evt)
	if contentBlocks(t, evt)[0] != "just a string" {
		t.Error("non-map content block should pass through")
	}
}
