package mcpgo

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// rawArgumentsAvailable reports whether the mcp-go this build links against
// preserves the original argument bytes (v0.56.0+). Tests that assert on
// >2^53 integer fidelity must gate on it: below v0.56.0 the decoded map is the
// only source and large integers legitimately round through float64.
func rawArgumentsAvailable() bool { return rawArgumentsField() != nil }

// echoRawArgs returns the bytes a fixture handler should echo back — the
// preserved wire bytes where this version keeps them, otherwise the decoded
// arguments re-marshaled. Fixtures use it instead of touching
// Params.RawArguments so they compile on every supported version.
func echoRawArgs(r mcp.CallToolRequest) []byte {
	if raw := rawArgumentBytes(r); len(raw) > 0 {
		return raw
	}
	out, err := json.Marshal(r.GetArguments())
	if err != nil {
		return nil
	}
	return out
}

// TestSetRawArgumentsWrites proves the reflective setter actually lands. A
// no-op setter would leave the pre-strip bytes in place, and a handler using
// BindArguments would still see AgentCat's injected params.
func TestSetRawArgumentsWrites(t *testing.T) {
	if !rawArgumentsAvailable() {
		t.Skip("mcp-go < v0.56.0 does not preserve raw arguments")
	}
	var params mcp.CallToolParams
	want := json.RawMessage(`{"kept":1}`)
	setRawArguments(&params, want)

	var req mcp.CallToolRequest
	req.Params = params
	if got := rawArgumentBytes(req); string(got) != string(want) {
		t.Errorf("setRawArguments did not land: got %q, want %q", got, want)
	}
}

// TestClearRawStructuredContentZeroes proves the reflective clear lands. If it
// silently no-oped, the preserved bytes would win at marshal time and the
// handle mirror would never reach the client.
func TestClearRawStructuredContentZeroes(t *testing.T) {
	if rawStructuredField() == nil {
		t.Skip("mcp-go < v0.56.0 does not preserve structured content bytes")
	}
	res := &mcp.CallToolResult{StructuredContent: map[string]any{"a": 1}}
	setRawStructuredForTest(t, res, json.RawMessage(`{"stale":true}`))
	if len(rawStructuredBytes(res)) == 0 {
		t.Fatal("test setup failed: raw structured bytes were not seeded")
	}

	clearRawStructuredContent(res)
	if got := rawStructuredBytes(res); len(got) != 0 {
		t.Errorf("clearRawStructuredContent left %q behind", got)
	}
}

// TestRawArgumentBytesReadsWireDecodedRequests pins the read half: on the
// pinned version GetRawArguments must actually yield json.RawMessage for a
// request decoded from the wire. If upstream changed its return type, every
// caller would silently fall back to the decoded map.
func TestRawArgumentBytesReadsWireDecodedRequests(t *testing.T) {
	if !rawArgumentsAvailable() {
		t.Skip("mcp-go < v0.56.0 does not preserve raw arguments")
	}
	var req mcp.CallToolRequest
	wire := []byte(`{"params":{"name":"t","arguments":{"id":1234567890123456789}}}`)
	if err := json.Unmarshal(wire, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	raw := rawArgumentBytes(req)
	if len(raw) == 0 {
		t.Fatal("GetRawArguments returned no json.RawMessage for a wire-decoded " +
			"request: its return type may have changed")
	}
	if string(raw) == "" || !json.Valid(raw) {
		t.Errorf("raw arguments are not valid JSON: %q", raw)
	}
}

// setRawStructuredForTest seeds RawStructuredContent reflectively so this file
// compiles against versions that lack the field.
func setRawStructuredForTest(t *testing.T, res *mcp.CallToolResult, raw json.RawMessage) {
	t.Helper()
	idx := rawStructuredField()
	if idx == nil {
		t.Skip("mcp-go < v0.56.0 does not preserve structured content bytes")
	}
	reflect.ValueOf(res).Elem().FieldByIndex(idx).Set(reflect.ValueOf(raw))
}
