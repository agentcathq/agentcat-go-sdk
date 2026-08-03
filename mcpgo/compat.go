package mcpgo

import (
	"encoding/json"
	"reflect"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
)

// This file is the ONLY place in the adapter allowed to reference an mcp-go
// API newer than the declared floor (v0.45.0). Everything else compiles
// against the floor unconditionally.
//
// Two fields arrived in mcp-go v0.56.0 to stop large integers losing precision
// through a float64 round trip: CallToolParams.RawArguments and
// CallToolResult.RawStructuredContent. Both preserve the original wire bytes
// and both WIN over their decoded counterpart at marshal time.
//
// On v0.45.0-v0.55.x neither exists, and the decoded field is the sole source
// of truth. That makes every shim here a no-op on those versions rather than a
// degradation: there are no preserved bytes to read, and none to invalidate.
// The only thing genuinely lost is the >2^53 precision guarantee, which is
// upstream's own behavior on versions that predate the fix.
//
// Absence is detected once per process, not per call. compat_test.go pins that
// detection against the pinned version, so an upstream rename fails a test
// instead of silently disabling telemetry.

// rawArgumentBytes returns the request's preserved argument bytes, or nil when
// this mcp-go version does not preserve them.
//
// No reflection needed: CallToolRequest.GetRawArguments() exists with an
// identical signature on every supported version. Only what it returns changed
// — json.RawMessage from v0.56.0, and the decoded Params.Arguments before
// that, which fails the type assertion and correctly reports "no raw bytes".
func rawArgumentBytes(r mcp.CallToolRequest) json.RawMessage {
	raw, _ := r.GetRawArguments().(json.RawMessage)
	return raw
}

// rawArgumentsField locates CallToolParams.RawArguments once. Invalid when the
// field does not exist (< v0.56.0).
var rawArgumentsField = sync.OnceValue(func() (idx []int) {
	f, ok := reflect.TypeOf(mcp.CallToolParams{}).FieldByName("RawArguments")
	if !ok || f.Type != reflect.TypeOf(json.RawMessage(nil)) {
		return nil
	}
	return f.Index
})

// setRawArguments writes the rebuilt argument bytes onto a dispatched request,
// or does nothing when this version has no such field.
//
// The write matters only from v0.56.0: there, BindArguments and MarshalJSON
// both PREFER RawArguments over Arguments, so leaving stale bytes behind would
// let a handler that binds its arguments see AgentCat's injected params after
// the strip already removed them from Arguments. Before v0.56.0 setting
// Params.Arguments alone completes the strip for every handler.
func setRawArguments(params *mcp.CallToolParams, raw json.RawMessage) {
	idx := rawArgumentsField()
	if idx == nil {
		return
	}
	field := reflect.ValueOf(params).Elem().FieldByIndex(idx)
	if !field.CanSet() {
		return
	}
	field.Set(reflect.ValueOf(raw))
}

// rawStructuredField locates CallToolResult.RawStructuredContent once. Invalid
// when the field does not exist (< v0.56.0).
var rawStructuredField = sync.OnceValue(func() (idx []int) {
	f, ok := reflect.TypeOf(mcp.CallToolResult{}).FieldByName("RawStructuredContent")
	if !ok || f.Type != reflect.TypeOf(json.RawMessage(nil)) {
		return nil
	}
	return f.Index
})

// rawStructuredBytes returns the result's preserved structuredContent bytes,
// or nil when this version does not preserve them. Callers fall back to
// StructuredContent, which is the only source of truth on those versions.
func rawStructuredBytes(res *mcp.CallToolResult) json.RawMessage {
	idx := rawStructuredField()
	if idx == nil || res == nil {
		return nil
	}
	field := reflect.ValueOf(res).Elem().FieldByIndex(idx)
	if !field.CanInterface() {
		return nil
	}
	raw, _ := field.Interface().(json.RawMessage)
	return raw
}

// clearRawStructuredContent invalidates the preserved bytes after a caller has
// edited StructuredContent, or does nothing when this version has no such
// field.
//
// From v0.56.0 CallToolResult.MarshalJSON emits the preserved bytes verbatim
// whenever they are non-empty and ignores StructuredContent entirely, so an
// edit that does not clear them never reaches the client. Before v0.56.0 there
// is nothing to win over the edit.
func clearRawStructuredContent(res *mcp.CallToolResult) {
	idx := rawStructuredField()
	if idx == nil || res == nil {
		return
	}
	field := reflect.ValueOf(res).Elem().FieldByIndex(idx)
	if !field.CanSet() {
		return
	}
	field.SetZero()
}
