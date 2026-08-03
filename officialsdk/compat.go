package officialsdk

import (
	"reflect"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	agentcat "go.agentcat.com/sdk/v2"
)

// This file is the ONLY place in the adapter allowed to reference a go-sdk API
// newer than the declared floor (v1.4.1). Everything else compiles against the
// floor unconditionally.
//
// Four APIs arrived together in go-sdk v1.7.0, all part of MCP 2026-07-28:
// ServerRequest.ProtocolVersion, ServerRequest.ClientInfo,
// CallToolParamsRaw.InputResponses and CallToolResult.NeedsInput. Three are
// methods, so an interface assertion detects them for free and without
// reflection; only InputResponses is a struct field.
//
// The two degradations are of different kinds, and the difference matters:
//
//   - Client identity is RECOVERABLE. v1.7.0's accessors are a ladder over
//     _meta then the initialize capture, and both rungs exist in every
//     supported version. The fallback below rebuilds that ladder, so identity
//     is not lost — it is derived the way v1 derived it.
//   - MRTR is NOT recoverable, and must not be. Multi-round-trip did not exist
//     before the 2026-07-28 protocol, so on an older go-sdk there are no
//     intermediate rounds to tag or to suppress mint-back for. Reporting
//     "no MRTR" there is the truth, not a compromise.
//
// compat_test.go pins the detection against the pinned version, so an upstream
// rename fails a test instead of silently disabling telemetry.

// protocolVersionOf returns the negotiated protocol version for this request.
//
// go-sdk v1.7.0+ answers directly. Older versions have no accessor, so the
// ladder is rebuilt: the reserved _meta key a 2026 client sends, then the
// version captured at initialize.
func protocolVersionOf(ctr *mcp.CallToolRequest) string {
	if pv, ok := any(ctr).(interface{ ProtocolVersion() string }); ok {
		return pv.ProtocolVersion()
	}
	if ctr == nil {
		return ""
	}
	if v := protocolVersionFromMeta(ctr.Params); v != "" {
		return v
	}
	if ctr.Session != nil {
		if init := ctr.Session.InitializeParams(); init != nil {
			return init.ProtocolVersion
		}
	}
	return ""
}

// protocolVersionFromMeta reads the reserved per-request _meta key a 2026
// client sends. The Meta map is embedded on CallToolParamsRaw in every
// supported version.
func protocolVersionFromMeta(params *mcp.CallToolParamsRaw) string {
	if params == nil {
		return ""
	}
	v, _ := params.GetMeta()[agentcat.MetaProtocolVersionKey].(string)
	return v
}

// clientInfoOf returns the calling client's identity, or nil when unknown.
//
// Same ladder as protocolVersionOf. The _meta rung is what lets a 2026 client
// identify itself per request; the initialize rung is what v1 used, and it is
// still the only source for a pre-2026 client.
func clientInfoOf(ctr *mcp.CallToolRequest) *mcp.Implementation {
	if ci, ok := any(ctr).(interface{ ClientInfo() *mcp.Implementation }); ok {
		return ci.ClientInfo()
	}
	if ctr == nil {
		return nil
	}
	if info := clientInfoFromMeta(ctr.Params); info != nil {
		return info
	}
	if ctr.Session != nil {
		if init := ctr.Session.InitializeParams(); init != nil && init.ClientInfo != nil {
			// Copy: the session's params outlive this request and the caller
			// must not be able to race event capture against them.
			cp := *init.ClientInfo
			return &cp
		}
	}
	return nil
}

// clientInfoFromMeta reads the reserved per-request _meta key a 2026 client
// sends, or nil when it is absent or malformed. This is the rung that lets a
// modern client identify itself on a server built against an older go-sdk.
func clientInfoFromMeta(params *mcp.CallToolParamsRaw) *mcp.Implementation {
	if params == nil {
		return nil
	}
	info, ok := params.GetMeta()[agentcat.MetaClientInfoKey].(map[string]any)
	if !ok {
		return nil
	}
	name, _ := info["name"].(string)
	version, _ := info["version"].(string)
	if name == "" && version == "" {
		return nil
	}
	return &mcp.Implementation{Name: name, Version: version}
}

// resultNeedsInput reports whether this result is an intermediate MRTR round
// awaiting client input. Always false before go-sdk v1.7.0, where the protocol
// had no such round.
//
// This gates more than a tag: an intermediate round must NOT carry mint-back
// text or the structured mirror, because the completing round carries them.
// False is the correct answer on versions that cannot produce one.
func resultNeedsInput(res *mcp.CallToolResult) bool {
	if res == nil {
		return false
	}
	if n, ok := any(res).(interface{ NeedsInput() bool }); ok {
		return n.NeedsInput()
	}
	return false
}

// inputResponsesField locates CallToolParamsRaw.InputResponses once. Invalid
// when the field does not exist (< v1.7.0).
var inputResponsesField = sync.OnceValue(func() (idx []int) {
	f, ok := reflect.TypeOf(mcp.CallToolParamsRaw{}).FieldByName("InputResponses")
	if !ok || f.Type.Kind() != reflect.Map {
		return nil
	}
	return f.Index
})

// hasInputResponses reports whether this call is an MRTR continuation — a
// retry carrying responses to a previous round's input requests. Always false
// before go-sdk v1.7.0.
func hasInputResponses(params *mcp.CallToolParamsRaw) bool {
	idx := inputResponsesField()
	if idx == nil || params == nil {
		return false
	}
	field := reflect.ValueOf(params).Elem().FieldByIndex(idx)
	return field.IsValid() && field.Kind() == reflect.Map && field.Len() > 0
}
