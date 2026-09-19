package tokens

import (
	"strconv"
	"strings"
	"testing"
)

// Shared vectors, pinned byte-for-byte against the TypeScript and Python
// SDKs. See the TypeScript repo's
// docs/superpowers/specs/2026-09-19-sdk-token-estimates-design.md.

func text(t string) map[string]any { return map[string]any{"type": "text", "text": t} }

func TestBytesPerTokenIsPinned(t *testing.T) {
	if BytesPerToken != 3.5 {
		t.Fatalf("BytesPerToken = %v, want 3.5", BytesPerToken)
	}
}

func TestEstimate(t *testing.T) {
	// bytes is int64, not int: the clamp row's byte count (7516192765)
	// overflows a 32-bit int, and Estimate takes an int, so that row is
	// appended only where int is 64 bits wide (strconv.IntSize). int64
	// holds the literal on every platform; only the eventual int(...)
	// conversion below would overflow on 32-bit, so that row is skipped
	// there instead of attempting it.
	cases := []struct {
		bytes int64
		want  int32
	}{
		{0, 0}, {1, 1}, {7, 2}, {13, 4}, {4096, 1171}, {-1, 0},
	}
	if strconv.IntSize == 64 {
		cases = append(cases, struct {
			bytes int64
			want  int32
		}{7516192765, 2147483647})
	}
	for _, c := range cases {
		if got := Estimate(int(c.bytes)); got != c.want {
			t.Errorf("Estimate(%d) = %d, want %d", c.bytes, got, c.want)
		}
	}
}

func TestInputTokens(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want int32
	}{
		{"plain", map[string]any{"q": "hello"}, 4},
		{"empty object", map[string]any{}, 1},
		{"accented", map[string]any{"name": "café"}, 5},
		{"html chars", map[string]any{"html": "<a>&</a>"}, 6}, // 44 bytes -> 13 with HTML escaping; must be 19 -> 6
		{"cjk", map[string]any{"t": "你好"}, 4},
		{"nested", map[string]any{"ids": []any{1, 2, 3}, "opts": map[string]any{"deep": true, "n": nil}}, 13},
	}
	for _, c := range cases {
		got, ok := InputTokens(c.args)
		if !ok || got != c.want {
			t.Errorf("%s: InputTokens = (%d, %v), want (%d, true)", c.name, got, ok, c.want)
		}
	}
}

func TestInputTokensAbsent(t *testing.T) {
	if _, ok := InputTokens(nil); ok {
		t.Error("nil arguments must be omitted")
	}
}

func TestInputTokensUnencodable(t *testing.T) {
	if _, ok := InputTokens(map[string]any{"fn": func() {}}); ok {
		t.Error("an unencodable value must be omitted, not counted")
	}
}

// panicky panics from inside encoding/json, the way a customer's own
// MarshalJSON can.
type panicky struct{}

func (panicky) MarshalJSON() ([]byte, error) { panic("marshal bug") }

func TestInputTokensNeverPanics(t *testing.T) {
	if _, ok := InputTokens(map[string]any{"v": panicky{}}); ok {
		t.Error("a panicking value must be omitted, not counted")
	}
}

func TestOutputTokensNeverPanics(t *testing.T) {
	// No content list, so the fallback encodes the whole response and hits
	// the panicking marshaler.
	if _, ok := OutputTokens(map[string]any{"v": panicky{}}); ok {
		t.Error("a panicking value must be omitted, not counted")
	}
}

func TestOutputTokens(t *testing.T) {
	cases := []struct {
		name     string
		response map[string]any
		want     int32
	}{
		{"one text block", map[string]any{"content": []any{text("hello world")}}, 4},
		{"summed before ceil", map[string]any{"content": []any{text("abcd"), text("e")}}, 2},
		{"empty text", map[string]any{"content": []any{text("")}}, 0},
		{"image only", map[string]any{"content": []any{map[string]any{"type": "image", "data": "QUJD", "mimeType": "image/png"}}}, 0},
		{"resource text", map[string]any{"content": []any{map[string]any{"type": "resource", "resource": map[string]any{"uri": "file:///a", "text": "resource body"}}}}, 4},
		{"resource blob", map[string]any{"content": []any{map[string]any{"type": "resource", "resource": map[string]any{"uri": "file:///a", "blob": "QUJD"}}}}, 0},
		{"structured ignored", map[string]any{"content": []any{text("hi")}, "structuredContent": map[string]any{"big": strings.Repeat("y", 1000)}}, 1},
		{"isError ignored", map[string]any{"content": []any{text("hi")}, "isError": true}, 1},
		{"4k text", map[string]any{"content": []any{text(strings.Repeat("x", 4096))}}, 1171},
		{"non-string text", map[string]any{"content": []any{map[string]any{"type": "text", "text": 42}, nil, "str"}}, 0},
		{"no content list falls back", map[string]any{"result": "ok"}, 5},
	}
	for _, c := range cases {
		got, ok := OutputTokens(c.response)
		if !ok || got != c.want {
			t.Errorf("%s: OutputTokens = (%d, %v), want (%d, true)", c.name, got, ok, c.want)
		}
	}
}

func TestOutputTokensAbsent(t *testing.T) {
	if _, ok := OutputTokens(nil); ok {
		t.Error("nil response must be omitted")
	}
}
