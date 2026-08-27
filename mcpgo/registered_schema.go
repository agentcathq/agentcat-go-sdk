package mcpgo

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"sync"
	"unsafe"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/v2"
)

// declareRegisteredSchemas declares AgentCat's own properties on the
// REGISTERED schemas of every tool the engine touches, so mcp-go's own
// validators accept the calls and results AgentCat's advertised schemas ask
// for. Both halves are needed, for symmetric reasons:
//
//   - INPUT: mcp-go validates a call's arguments against the tool's registered
//     input schema (server/server.go:1991) BEFORE any Use() middleware runs, so
//     AgentCat cannot strip the injected parameters first. A server built with
//     WithInputSchemaValidation plus a closed schema (WithStrictInputSchemaDefault,
//     or mcp.WithSchemaAdditionalProperties(false)) would reject every call that
//     carries session_id / agent_id / context — which is exactly what the advertised
//     schema tells a schema-following agent to send. The parameters are declared
//     OPTIONAL here and never added to the registered `required` list: a call that
//     omits them must still succeed.
//
//   - OUTPUT: mcp-go validates a result against the registered output schema
//     AFTER the middleware has run (server/server.go:2021), and
//     mcp.WithOutputSchema[T] emits "additionalProperties": false, so the
//     structured mirror needs `mcp_session` declared there.
//
// A declared property satisfies a closed schema, so `additionalProperties` is
// deliberately left exactly as the customer set it on both halves.
//
// This runs on every tools/list (tools can be registered after Track), so it
// must be free when there is nothing to do: schemaDeclarations returns no
// entry for a tool whose registered schema already carries exactly the wanted
// bytes, and what it does return is written in place rather than
// re-registered. A customer who declares any of these names themselves keeps
// their own definition.
func declareRegisteredSchemas(mcpServer *server.MCPServer, cfg agentcat.InjectConfig) {
	defer func() {
		if r := recover(); r != nil {
			agentcat.LogRecoveredPanic("mcpgo registered schema declaration", r)
		}
	}()

	if mcpServer == nil {
		return
	}
	registered := mcpServer.ListTools()
	entries := make([]server.ServerTool, 0, len(registered))
	for _, entry := range registered {
		if entry != nil {
			entries = append(entries, *entry)
		}
	}

	if decls := schemaDeclarations(entries, cfg); len(decls) > 0 {
		// MCPServer.ListTools returns copies (server/server.go:1032-1046), so
		// the change has to reach the live entry for the validator to see it.
		// It is written IN PLACE under mcp-go's own tools lock rather than via
		// AddTools — see applyRegisteredDeclarations for why that matters. The
		// compiled-schema caches are keyed by (tool name, schema digest)
		// (server/input_validation.go:78-105), so a changed schema compiles
		// fresh with no stale entry to invalidate; AddTools does not invalidate
		// them either (only SetTools and DeleteTools do, server/server.go:1012,
		// :1064), so nothing is lost by bypassing it.
		applyRegisteredDeclarations(mcpServer, decls)
	}
}

// declareSessionSchemas is declareRegisteredSchemas for session-scoped tools
// (server.SessionWithTools), which live outside the global tool map and are
// validated against their own registered schema (server/server.go:1903-1913).
// Runs from the list hook, the only AgentCat seam that has the session in
// context.
//
// Unlike the global path this must go through AddSessionTools, which notifies
// the session. SessionWithTools is a CUSTOMER-implementable interface; its
// only contract on GetSessionTools is that it "must be thread-safe for
// concurrent access" (server/session.go:35-43), and both built-in
// implementations satisfy that by returning a copy (server/sse.go:136-145,
// server/streamable_http.go:1462-1468). So there is no map AgentCat may treat
// as live and no mutex of mcp-go's to hold — reaching into one particular
// implementation's internals would be unsound for every other. The equality
// skip in schemaDeclarations is therefore the whole protection here: it makes
// the declaration happen once per session tool rather than once per
// tools/list.
func declareSessionSchemas(ctx context.Context, mcpServer *server.MCPServer, cfg agentcat.InjectConfig) {
	defer func() {
		if r := recover(); r != nil {
			agentcat.LogRecoveredPanic("mcpgo session schema declaration", r)
		}
	}()

	if mcpServer == nil {
		return
	}
	session := server.ClientSessionFromContext(ctx)
	if session == nil {
		return
	}
	withTools, ok := session.(server.SessionWithTools)
	if !ok {
		return
	}

	sessionTools := withTools.GetSessionTools()
	entries := make([]server.ServerTool, 0, len(sessionTools))
	byName := make(map[string]server.ServerTool, len(sessionTools))
	for _, entry := range sessionTools {
		entries = append(entries, entry)
		byName[entry.Tool.Name] = entry
	}

	decls := schemaDeclarations(entries, cfg)
	if len(decls) == 0 {
		return
	}
	changed := make([]server.ServerTool, 0, len(decls))
	for _, decl := range decls {
		entry, ok := byName[decl.name]
		if !ok {
			continue
		}
		decl.applyTo(&entry.Tool)
		changed = append(changed, entry)
	}
	if len(changed) > 0 {
		_ = mcpServer.AddSessionTools(session.SessionID(), changed...)
	}
}

// schemaDeclaration is one tool's pending registered-schema change: the bytes
// to write, and the bytes the tool carried when the declaration was computed.
// The latter is the compare-and-set guard: a live entry that no longer matches
// was re-registered by someone else and must not be clobbered.
type schemaDeclaration struct {
	name string

	setInput  bool
	fromInput []byte
	rawInput  json.RawMessage

	setOutput  bool
	fromOutput []byte
	rawOutput  json.RawMessage
}

// applyTo writes the declared halves onto a tool. mcp-go's Tool.MarshalJSON
// errors when a structured and a raw schema are both set, so the structured
// field is always cleared alongside.
func (d schemaDeclaration) applyTo(tool *mcp.Tool) {
	if d.setInput {
		tool.InputSchema = mcp.ToolInputSchema{}
		tool.RawInputSchema = d.rawInput
	}
	if d.setOutput {
		tool.OutputSchema = mcp.ToolOutputSchema{}
		tool.RawOutputSchema = d.rawOutput
	}
}

// schemaDeclarations returns the pending changes for the tools whose
// registered schemas do not already carry exactly the declaration AgentCat
// wants. The engine decides what those properties are and what they look like,
// so the registered and advertised copies can never drift.
//
// A tool whose registered schema ALREADY equals the wanted bytes produces no
// entry, so repeated passes over an unchanged tool set return nothing at all.
// That skip is load-bearing and must not be weakened: it is the difference
// between declaring once and re-declaring on every tools/list. The engine
// cannot supply the skip on its own — it is idempotent by design, so it
// re-asserts AgentCat's own properties rather than reporting "nothing to do",
// which is precisely what keeps argument stripping working.
func schemaDeclarations(entries []server.ServerTool, cfg agentcat.InjectConfig) []schemaDeclaration {
	if len(entries) == 0 {
		return nil
	}

	// Deterministic order so the pass is reproducible.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Tool.Name < entries[j].Tool.Name })

	byName := make(map[string]server.ServerTool, len(entries))
	tools := make([]mcp.Tool, 0, len(entries))
	for _, entry := range entries {
		byName[entry.Tool.Name] = entry
		tools = append(tools, entry.Tool)
	}

	normalized := normalizeMCPGoTools(tools)
	injected, reg := agentcat.BuildInjectedTools(cfg, normalized)

	original := make(map[string]agentcat.NormalizedTool, len(normalized))
	for _, nt := range normalized {
		original[nt.Name] = nt
	}

	var decls []schemaDeclaration
	for _, nt := range injected {
		entry, ok := byName[nt.Name]
		if !ok {
			continue
		}
		decl := schemaDeclaration{
			name:       nt.Name,
			fromInput:  registeredInputBytes(entry.Tool),
			fromOutput: registeredOutputBytes(entry.Tool),
		}

		// mcp-go's validator prefers RawInputSchema, and
		// applyStrictInputSchemaDefault skips tools that carry one — so writing
		// the raw form is also what pins additionalProperties to the customer's
		// own value.
		if raw, ok := registeredInputDeclaration(original[nt.Name], reg.InjectedParams[nt.Name], nt.InputSchema); ok &&
			!bytes.Equal(raw, decl.fromInput) {
			decl.setInput = true
			decl.rawInput = raw
		}

		if reg.OutputInjected[nt.Name] && nt.OutputSchema != nil {
			if raw, err := json.Marshal(nt.OutputSchema); err == nil && !bytes.Equal(raw, decl.fromOutput) {
				decl.setOutput = true
				decl.rawOutput = raw
			}
		}

		if decl.setInput || decl.setOutput {
			decls = append(decls, decl)
		}
	}
	return decls
}

// registeredInputBytes renders a registered tool's input schema exactly as
// mcp-go's validator reads it: RawInputSchema when set, otherwise the
// marshaled structured schema. Returns nil when neither is usable, which never
// equals a declaration and so never suppresses one.
func registeredInputBytes(tool mcp.Tool) []byte {
	if len(tool.RawInputSchema) > 0 {
		return tool.RawInputSchema
	}
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return nil
	}
	return raw
}

// registeredOutputBytes is registeredInputBytes for the output half. A tool
// with no output schema at all renders as nil.
func registeredOutputBytes(tool mcp.Tool) []byte {
	if len(tool.RawOutputSchema) > 0 {
		return tool.RawOutputSchema
	}
	if tool.OutputSchema.Type == "" {
		return nil
	}
	raw, err := json.Marshal(tool.OutputSchema)
	if err != nil {
		return nil
	}
	return raw
}

// applyRegisteredDeclarations writes the declared schemas onto mcp-go's LIVE
// tool entries, under mcp-go's own tools lock.
//
// It deliberately does NOT go through MCPServer.AddTools. AddTools broadcasts
// notifications/tools/list_changed whenever the listChanged capability is set,
// and mcp-go turns that on by default for any server with tools
// (implicitlyRegisterToolCapabilities, server/server.go:910-915). Since this
// declaration pass runs from the tools/list hook, any re-registration there
// tells every connected client "the tool list changed" — and a client that
// re-lists on that notification, which the spec recommends, lists again,
// declares again, notifies again. AgentCat would be the sole cause of a
// self-sustaining storm inside its customers' servers. Writing in place makes
// that notification structurally impossible rather than merely unlikely, which
// is why it is preferred over guarding the AddTools call: it removes the class
// of failure, not one instance of it.
//
// Two further things fall out of doing it this way. Bypassing AddTools also
// bypasses applyStrictInputSchemaDefault (server/server.go:884-894), which is
// desirable — it would otherwise re-close a schema behind us. And reading the
// live entry and writing it back happen under a single acquisition of the
// write lock, with a compare against what this pass was computed from, so a
// concurrent AddTools/SetTools/DeleteTools cannot be clobbered by a stale
// declaration: an entry that changed underneath us is skipped and picked up on
// the next list.
//
// Lock discipline: the two callers are Track (no request in flight) and the
// AfterListTools hook, which mcp-go fires after handleListTools has returned
// (server/request_handler.go:387) — filteredTools releases toolsMu before that
// (server/server.go:1792). Neither holds the lock, so taking it here cannot
// deadlock.
//
// Returns false when the private fields could not be reached, in which case
// nothing is written. TestLiveRegisteredTools_ReadsPrivateToolState is the
// drift tripwire.
func applyRegisteredDeclarations(mcpServer *server.MCPServer, decls []schemaDeclaration) bool {
	return withLiveTools(mcpServer, func(live map[string]server.ServerTool) {
		for _, decl := range decls {
			current, exists := live[decl.name]
			if !exists {
				continue // deleted since this pass was computed
			}
			// Only write when the live schema is still the one this
			// declaration was derived from; otherwise something re-registered
			// the tool and their version wins.
			if !bytes.Equal(registeredInputBytes(current.Tool), decl.fromInput) ||
				!bytes.Equal(registeredOutputBytes(current.Tool), decl.fromOutput) {
				continue
			}
			decl.applyTo(&current.Tool)
			live[decl.name] = current
		}
	})
}

// withLiveTools calls fn with mcp-go's LIVE registered tool map, holding
// mcp-go's own write lock for exactly the duration of the call. It reports
// false, having called nothing, when the private state could not be reached.
//
// The map is passed to a callback rather than returned so the DEREFERENCE
// cannot escape the critical section: locateToolState hands out only a
// pointer, and this is its one caller. That is the point of the shape — the
// invariant "this map is only ever read or written under toolsMu" is enforced
// by the API rather than by every future caller remembering it.
// MCPServer.SetTools reassigns the map field (server/server.go:1010, the only
// site that does), so even READING it outside the lock is a data race — one
// that would surface as a -race report inside a customer's own test suite,
// pointing at AgentCat.
//
// The compare-and-set inside fn cannot substitute for the lock: it compares
// entries WITHIN whatever map it was handed, so it detects a re-registered
// tool but not a wholesale swap of the map.
//
// Two obligations the API cannot enforce, both on fn:
//
//   - fn must not RETAIN the map. Go cannot stop a callback assigning it to
//     something outer, and a copy of the reference used after this returns
//     would race with mcp-go's own writers exactly as an unsynchronized read
//     would. Use it and let it go.
//   - fn must not call back into mcpServer: toolsMu is held, so anything
//     touching the tool map would deadlock.
//
// Callers do pure in-memory edits and satisfy both.
//
// Sanctioned private access, guarded by a drift tripwire test, exactly like
// officialsdk's getServerImpl. mcp-go v0.57.0 exposes no seam for mutating a
// registered tool without re-registering it, and re-registering notifies every
// client — see applyRegisteredDeclarations' caller.
func withLiveTools(s *server.MCPServer, fn func(map[string]server.ServerTool)) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			agentcat.LogRecoveredPanic("mcpgo live tool map access", r)
			ok = false
		}
	}()

	mu, toolsPtr, located := locateToolState(s)
	if !located {
		agentcat.LogWarn("mcpgo: could not reach mcp-go's registered tool map; schema declarations were not applied (mcp-go internals may have changed)")
		return false
	}

	mu.Lock()
	defer mu.Unlock()

	// First read of any field DATA, and it happens here, under the lock.
	live := *toolsPtr
	if live == nil {
		return false
	}
	fn(live)
	return true
}

// locateToolState finds mcp-go's unexported tool map and the mutex guarding it
// (server/server.go:192,209), WITHOUT reading either.
//
// Everything here is address arithmetic and type inspection: FieldByName
// computes an offset, UnsafeAddr adds it to the struct pointer, and Type reads
// the struct's type descriptor. No field data is touched, which is what makes
// it legal to run before the lock is held. Only withLiveTools may dereference
// the returned pointer, and only under that lock.
//
// Limit of the tripwire, inherent to reflection: it detects a renamed or
// retyped field, but NOT mcp-go keeping both names while moving `tools` under
// a different mutex. Reflection would still succeed and the lock would simply
// be the wrong one, silently. Re-check the pairing when bumping mcp-go.
func locateToolState(s *server.MCPServer) (mu *sync.RWMutex, toolsPtr *map[string]server.ServerTool, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			mu, toolsPtr, ok = nil, nil, false
		}
	}()
	if s == nil {
		return nil, nil, false
	}
	v := reflect.ValueOf(s).Elem()

	// The mutex first: it is what makes reading the map field legal, and
	// deriving it touches no field data of its own.
	muField := v.FieldByName("toolsMu")
	if !muField.IsValid() || muField.Type() != reflect.TypeOf(sync.RWMutex{}) {
		return nil, nil, false
	}
	mu = (*sync.RWMutex)(unsafe.Pointer(muField.UnsafeAddr()))

	// The map field's TYPE is checked against the exact expected type rather
	// than by asserting on a read value, so the check itself stays read-free.
	toolsField := v.FieldByName("tools")
	if !toolsField.IsValid() || toolsField.Type() != reflect.TypeOf(map[string]server.ServerTool(nil)) {
		return nil, nil, false
	}
	toolsPtr = (*map[string]server.ServerTool)(unsafe.Pointer(toolsField.UnsafeAddr()))

	return mu, toolsPtr, true
}

// registeredInputDeclaration renders the registered input schema for one tool:
// the customer's own schema plus the injected parameters declared as OPTIONAL
// properties. It returns ok=false when there is nothing to add.
//
// The property schemas come from the engine's own output (`injectedSchema`),
// which is what keeps the registered and advertised declarations byte-identical
// — the engine re-asserts a property it recognises as its own, so the next
// tools/list still records these parameters as injected and stripping keeps
// working.
//
// `required` and `additionalProperties` are copied from the customer's schema
// untouched: requiring a parameter here would reject every call that omits it,
// which is the opposite of what this exists to prevent.
func registeredInputDeclaration(
	original agentcat.NormalizedTool,
	injectedParams []string,
	injectedSchema *agentcat.SchemaObject,
) (json.RawMessage, bool) {
	if len(injectedParams) == 0 || original.InputSchema == nil || injectedSchema == nil {
		return nil, false
	}
	advertisedProps, ok := propertiesOf(injectedSchema)
	if !ok {
		return nil, false
	}

	registered := original.InputSchema.Clone()
	props := agentcat.NewSchemaObject()
	if raw, has := registered.Get("properties"); has {
		parsed, err := agentcat.ParseSchemaObject(raw)
		if err != nil {
			return nil, false
		}
		props = parsed
	}

	added := false
	for _, name := range injectedParams {
		declaration, has := advertisedProps.Get(name)
		if !has || props.Has(name) {
			continue
		}
		props.Set(name, declaration)
		added = true
	}
	if !added {
		return nil, false
	}

	propsRaw, err := json.Marshal(props)
	if err != nil {
		return nil, false
	}
	registered.Set("properties", propsRaw)
	raw, err := json.Marshal(registered)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// propertiesOf parses a schema's "properties" member.
func propertiesOf(schema *agentcat.SchemaObject) (*agentcat.SchemaObject, bool) {
	raw, has := schema.Get("properties")
	if !has {
		return agentcat.NewSchemaObject(), true
	}
	parsed, err := agentcat.ParseSchemaObject(raw)
	if err != nil {
		return nil, false
	}
	return parsed, true
}
