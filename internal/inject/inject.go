package inject

import (
	"bytes"
	"encoding/json"
	"slices"

	"go.agentcat.com/sdk/v2/internal/constants"
	"go.agentcat.com/sdk/v2/internal/handles"
	"go.agentcat.com/sdk/v2/internal/logging"
)

var logger = logging.New()

// NormalizedTool is the engine's library-neutral view of a tool. Adapters
// keep the full tool objects and write mutated schemas back after Build.
//
// A nil schema and an *Opaque schema are different things and must not be
// conflated. Nil means the tool declares nothing at that position; opaque
// means it declares something this SDK could not read (unparseable JSON, a
// non-object document, a value that would not marshal). Build injects into
// neither, but an opaque schema is one AgentCat must be especially careful
// with: it is a real customer contract that simply cannot be inspected, so it
// passes through byte-for-byte and the adapter must not write anything back
// over it.
type NormalizedTool struct {
	Name         string
	InputSchema  *SchemaObject // nil when the tool declares no input schema
	OutputSchema *SchemaObject // nil when absent or not a plain JSON object

	// InputSchemaOpaque marks a declared input schema the adapter could not
	// parse. Build leaves the tool alone and records an empty registry entry.
	InputSchemaOpaque bool

	// OutputSchemaOpaque is InputSchemaOpaque for the output half.
	OutputSchemaOpaque bool
}

// Config selects what Build injects. It is derived from Options once per
// list/rebuild; identical Config + tools must always produce identical
// output (rebuild-on-demand depends on it).
type Config struct {
	InjectHandles      bool // false when tracing is disabled
	HookMode           bool // ResolveSessionID configured: never inject session_id
	AgentTracking      bool // inject agent_id, marked required
	InjectContext      bool
	ContextDescription string
}

// Registries record exactly what Build injected, per tool. They drive
// argument stripping and structured-mirror gating.
type Registries struct {
	InjectedParams map[string][]string `json:"injectedParams"`
	OutputInjected map[string]bool     `json:"outputInjected"`

	// CustomerOwnedParams names, per tool, the injectable parameters the
	// customer declared themselves — so Build left them alone.
	//
	// Recorded as DATA rather than reported as a side effect: Build is
	// deterministic and runs on every tools/list and every on-demand rebuild,
	// so logging from inside it would repeat for the life of the process and
	// would put mutable reporting state inside a pure function. The call path
	// reports from this instead, deduplicated per server.
	CustomerOwnedParams map[string][]string `json:"customerOwnedParams"`
}

// SessionParamIsOurs reports whether the session_id argument on a call to
// toolName is AgentCat's to read, or the customer's to leave alone.
//
// A tool MISSING from the registry counts as ours. A call can legitimately
// land before any tools/list — a fresh per-request factory instance, a client
// that skips discovery — and disowning it there would silently stop
// validating every such call.
//
// A tool that WAS listed but has no session_id among its injected parameters
// is not ours. That covers the customer declaring the parameter themselves,
// and also the schemas Build declines to touch at all (composed, opaque,
// malformed), which record an empty entry. Both publish sessionless. This
// matches the TypeScript SDK exactly; see MIGRATION.md, which documents the
// composed-schema case as an observable behavior change.
func SessionParamIsOurs(toolName string, reg *Registries) bool {
	if reg == nil {
		return true
	}
	params, seen := reg.InjectedParams[toolName]
	if !seen {
		return true
	}
	return slices.Contains(params, constants.ParamSessionID)
}

// MergeRegistries returns a new Registries carrying every tool in prev plus
// every tool in next, with next winning on the tools it names.
//
// One tools/list rarely describes every tool a server can dispatch: a
// paginated list returns one page, tool filters and session-scoped tools vary
// the list per request. Replacing the registries with the last page seen
// would leave every other tool with no entry at all, and Strip deletes
// nothing for a tool it has no entry for — so injected parameters would reach
// the customer's handler. Merging keeps every tool that has ever been listed
// strippable.
//
// next wins per TOOL rather than per parameter so a tool whose schema
// legitimately changed (a re-registered tool that now declares its own
// session_id, say) is corrected rather than accumulating a stale union. prev is
// never mutated: the caller may be racing another list on the same slot.
func MergeRegistries(prev, next *Registries) *Registries {
	if next == nil {
		return prev
	}
	if prev == nil {
		return next
	}
	merged := &Registries{
		InjectedParams:      make(map[string][]string, len(prev.InjectedParams)+len(next.InjectedParams)),
		OutputInjected:      make(map[string]bool, len(prev.OutputInjected)+len(next.OutputInjected)),
		CustomerOwnedParams: make(map[string][]string, len(prev.CustomerOwnedParams)+len(next.CustomerOwnedParams)),
	}
	for name, params := range prev.InjectedParams {
		merged.InjectedParams[name] = params
	}
	for name, params := range prev.CustomerOwnedParams {
		merged.CustomerOwnedParams[name] = params
	}
	for name, injected := range prev.OutputInjected {
		merged.OutputInjected[name] = injected
	}
	for name, params := range next.InjectedParams {
		merged.InjectedParams[name] = params
		// Same per-tool replacement rule: a re-registered tool that no longer
		// declares session_id must lose its recorded collision.
		delete(merged.CustomerOwnedParams, name)
		// A tool present in this list but no longer output-injected must lose
		// its mirror gate, so the output half is keyed off the same names.
		delete(merged.OutputInjected, name)
	}
	for name, injected := range next.OutputInjected {
		merged.OutputInjected[name] = injected
	}
	for name, params := range next.CustomerOwnedParams {
		merged.CustomerOwnedParams[name] = params
	}
	return merged
}

var composedKeys = [...]string{"oneOf", "allOf", "anyOf"}

func isComposed(s *SchemaObject) bool {
	for _, k := range composedKeys {
		if s.Has(k) {
			return true
		}
	}
	return false
}

func stringParamSchema(description string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"type": "string", "description": description})
	return b
}

// instructionsPropertySchema is the optional _mcp_instructions property
// declared on plain-object output schemas.
func instructionsPropertySchema() json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"type":        "object",
		"description": constants.MCPInstructionsFieldDescription,
		"properties": map[string]any{
			constants.ParamSessionID:        map[string]string{"type": "string", "description": constants.MCPInstructionsSessionIDDescription},
			constants.ParamAgentID:          map[string]string{"type": "string", "description": constants.MCPInstructionsAgentIDDescription},
			constants.MirrorInstructionsKey: map[string]string{"type": "string"},
		},
	})
	return b
}

// Build runs the pure injection pipeline. It never mutates its inputs and
// never fails a tool list: problem tools pass through untouched with an
// empty registry entry.
//
// Build is IDEMPOTENT: Build(cfg, Build(cfg, tools)) == Build(cfg, tools). A
// property already declared with byte-exactly the schema this pipeline would
// write is treated as the pipeline's own — re-asserted (kept, required where
// the pipeline requires it, and recorded in the registry) rather than skipped
// as a customer-owned collision. That is what lets an adapter declare the
// injected parameters on its library's own registered schema (mcp-go
// validates arguments against that copy before any middleware can strip them)
// without the next tools/list mistaking AgentCat's own declaration for a
// customer's and silently disabling stripping. A property the customer
// declared with any other shape is still theirs and is left untouched.
func Build(cfg Config, tools []NormalizedTool) ([]NormalizedTool, *Registries) {
	reg := &Registries{
		InjectedParams:      make(map[string][]string, len(tools)),
		OutputInjected:      make(map[string]bool),
		CustomerOwnedParams: make(map[string][]string),
	}
	out := make([]NormalizedTool, 0, len(tools))

	for _, t := range tools {
		injected := []string{}
		result := NormalizedTool{
			Name:               t.Name,
			InputSchema:        t.InputSchema,
			OutputSchema:       t.OutputSchema,
			InputSchemaOpaque:  t.InputSchemaOpaque,
			OutputSchemaOpaque: t.OutputSchemaOpaque,
		}

		in := t.InputSchema
		switch {
		case t.InputSchemaOpaque:
			// The tool declares an input schema this SDK could not read.
			// Injecting into a schema we cannot see would mean replacing the
			// customer's contract with a guess, so pass it through.
			logger.Warnf("inject: tool %q has an unreadable input schema; advertising it untouched and skipping parameter injection", t.Name)
			in = nil
		case in == nil:
			// The tool declares no input schema at all. Neither supported MCP
			// library can produce this (mcp-go always marshals a
			// ToolInputSchema; the official SDK's AddTool panics without one),
			// so rather than synthesise a contract the customer never wrote,
			// leave the tool exactly as it is.
			in = nil
		case isComposed(in):
			logger.Warnf("inject: tool %q has a composed input schema (oneOf/allOf/anyOf); skipping parameter injection", t.Name)
			in = nil
		default:
			in = in.Clone()
		}

		if in != nil {
			props, required, ok := parsePropsAndRequired(t.Name, in)
			if ok {
				if raw, has := in.Get("additionalProperties"); has && bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
					in.Delete("additionalProperties")
				}

				if cfg.InjectHandles && !cfg.HookMode {
					want := stringParamSchema(constants.SessionIDParamDescription)
					if claimProperty(props, constants.ParamSessionID, want) {
						props.Set(constants.ParamSessionID, want)
						injected = append(injected, constants.ParamSessionID)
					} else {
						// Recorded, not logged: the call path reports this
						// once per tool with remediation. Escalated above a
						// warning because it costs the customer correlation.
						reg.CustomerOwnedParams[t.Name] = append(reg.CustomerOwnedParams[t.Name], constants.ParamSessionID)
					}
				}
				if cfg.InjectHandles && cfg.AgentTracking {
					desc := constants.AgentIDParamDescription
					if cfg.HookMode {
						desc = constants.AgentIDParamDescriptionHookMode
					}
					want := stringParamSchema(desc)
					if claimProperty(props, constants.ParamAgentID, want) {
						props.Set(constants.ParamAgentID, want)
						injected = append(injected, constants.ParamAgentID)
						required = appendUnique(required, constants.ParamAgentID)
					} else {
						reg.CustomerOwnedParams[t.Name] = append(reg.CustomerOwnedParams[t.Name], constants.ParamAgentID)
						logger.Warnf("inject: tool %q already defines %q; skipping injection of that parameter", t.Name, constants.ParamAgentID)
					}
				}
				if cfg.InjectContext {
					want := stringParamSchema(cfg.ContextDescription)
					if claimProperty(props, constants.ParamContext, want) {
						props.Set(constants.ParamContext, want)
						injected = append(injected, constants.ParamContext)
						required = appendUnique(required, constants.ParamContext)
					} else {
						// get_more_tools (and any customer tool) defining its
						// own context keeps it; nothing to inject and nothing
						// to report — context costs no correlation.
						reg.CustomerOwnedParams[t.Name] = append(reg.CustomerOwnedParams[t.Name], constants.ParamContext)
					}
				}

				propsRaw, err := json.Marshal(props)
				if err == nil {
					in.Set("properties", propsRaw)
					if len(required) > 0 {
						reqRaw, _ := json.Marshal(required)
						in.Set("required", reqRaw)
					}
					result.InputSchema = in
				} else {
					injected = injected[:0] // marshal failure: pass original through
				}
			}
		}

		if cfg.InjectHandles {
			if t.OutputSchemaOpaque {
				logger.Warnf("inject: tool %q has an unreadable output schema; advertising it untouched and leaving mint-back content-only", t.Name)
			} else if outSchema, did := injectOutput(t.Name, t.OutputSchema); did {
				result.OutputSchema = outSchema
				reg.OutputInjected[t.Name] = true
			}
		}

		reg.InjectedParams[t.Name] = injected
		out = append(out, result)
	}

	return out, reg
}

// claimProperty reports whether the pipeline may write want at name: either
// nothing is declared there, or what is declared is byte-exactly the
// pipeline's own value (see Build's idempotence contract). Anything else
// belongs to the customer and is left alone.
func claimProperty(props *SchemaObject, name string, want json.RawMessage) bool {
	existing, has := props.Get(name)
	if !has {
		return true
	}
	return sameJSON(existing, want)
}

// sameJSON compares two JSON documents for semantic equality, so a value that
// round-tripped through a customer's transport (re-indented, keys reordered)
// still matches the bytes this package produced.
func sameJSON(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	ca := canonicalJSON(a)
	if len(ca) == 0 {
		return false
	}
	return bytes.Equal(ca, canonicalJSON(b))
}

// canonicalJSON re-marshals raw with sorted keys and no insignificant
// whitespace, or returns nil when raw is not valid JSON.
func canonicalJSON(raw json.RawMessage) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// appendUnique appends name unless the list already carries it. Required
// lists must stay duplicate-free for Build to be idempotent.
func appendUnique(list []string, name string) []string {
	for _, v := range list {
		if v == name {
			return list
		}
	}
	return append(list, name)
}

// parsePropsAndRequired extracts the properties object and required list
// from a cloned input schema. ok=false means the schema is malformed; the
// caller passes the tool through untouched.
func parsePropsAndRequired(toolName string, in *SchemaObject) (*SchemaObject, []string, bool) {
	props := NewSchemaObject()
	if raw, has := in.Get("properties"); has {
		parsed, err := ParseSchemaObject(raw)
		if err != nil {
			logger.Warnf("inject: tool %q has malformed properties; skipping injection: %v", toolName, err)
			return nil, nil, false
		}
		props = parsed
	}
	var required []string
	if raw, has := in.Get("required"); has {
		if err := json.Unmarshal(raw, &required); err != nil {
			logger.Warnf("inject: tool %q has malformed required; skipping injection: %v", toolName, err)
			return nil, nil, false
		}
	}
	return props, required, true
}

// injectOutput declares the optional _mcp_instructions property on a
// plain-object output schema. Returns (schema, false) untouched for absent,
// composed, non-object, or customer-owned-key schemas. Output
// additionalProperties is never modified: the declared property is what keeps
// validators happy.
func injectOutput(toolName string, out *SchemaObject) (*SchemaObject, bool) {
	if out == nil {
		return nil, false
	}
	if isComposed(out) {
		logger.Warnf("inject: tool %q has a composed output schema; mint-back stays content-only for it", toolName)
		return out, false
	}
	if !permitsObject(out) {
		// The mirror is written into structuredContent as a JSON object
		// member, so a schema that declares a non-object result could never
		// carry it. Declaring `properties` there would be meaningless at best
		// and contract-breaking at worst.
		logger.Warnf("inject: tool %q declares a non-object output schema; mint-back stays content-only for it", toolName)
		return out, false
	}
	clone := out.Clone()
	props := NewSchemaObject()
	if raw, has := clone.Get("properties"); has {
		parsed, err := ParseSchemaObject(raw)
		if err != nil {
			return out, false
		}
		props = parsed
	}
	want := instructionsPropertySchema()
	if !claimProperty(props, constants.MCPInstructionsKey, want) {
		// Customer data — it wins; skip.
		return out, false
	}
	props.Set(constants.MCPInstructionsKey, want)
	propsRaw, err := json.Marshal(props)
	if err != nil {
		return out, false
	}
	clone.Set("properties", propsRaw)
	return clone, true
}

// permitsObject reports whether a schema's declared "type" allows a JSON
// object instance. An absent type constrains nothing, so it qualifies; a type
// union qualifies when "object" is one of its members.
func permitsObject(s *SchemaObject) bool {
	raw, has := s.Get("type")
	if !has {
		return true
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single == "object"
	}
	var union []string
	if err := json.Unmarshal(raw, &union); err == nil {
		for _, t := range union {
			if t == "object" {
				return true
			}
		}
	}
	return false
}

// Strip returns a copy of args with only the params the registry says were
// injected for this tool removed. With no registry at all (a call landed
// before any list and rebuild failed), it strips all three injectable names
// heuristically — except get_more_tools, whose context is a real parameter.
func Strip(toolName string, args map[string]any, reg *Registries) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	if reg == nil {
		delete(out, constants.ParamSessionID)
		delete(out, constants.ParamAgentID)
		if toolName != constants.GetMoreToolsName {
			delete(out, constants.ParamContext)
		}
		return out
	}
	for _, p := range reg.InjectedParams[toolName] {
		delete(out, p)
	}
	return out
}

// MirrorInput describes what the structured mirror may name for this call.
// The resolution alone decides whether a session may be named, so the mirror
// and the text block can never disagree about it.
type MirrorInput struct {
	Resolution handles.SessionResolution
	AgentID    string // supplied agent_id verbatim; "" when absent
}

// BuildMirror assembles the _mcp_instructions value mirrored into
// structuredContent on every response. Returns nil when there is nothing
// the agent could echo.
//
// A session is echoable only when it is one this SDK issued and the agent has
// a parameter to send it back on. That rules out hook mode (no parameter
// exists) and invalid (the value was rejected, so echoing it would confirm an
// ID this server never issued — the exact loop validation exists to break).
// A foreign call mirrors nothing at all: the parameter belongs to the
// customer's tool, so AgentCat has no standing to speak about it.
func BuildMirror(in MirrorInput) map[string]any {
	res := in.Resolution
	if res.Source == handles.SessionSourceForeign {
		return nil
	}

	sessionEchoable := !res.HookMode &&
		res.Source != handles.SessionSourceInvalid &&
		res.SessionID != ""

	m := map[string]any{}
	var names []string
	if sessionEchoable {
		m[constants.ParamSessionID] = res.SessionID
		names = append(names, constants.ParamSessionID)
	}
	if in.AgentID != "" {
		m[constants.ParamAgentID] = in.AgentID
		names = append(names, constants.ParamAgentID)
	}

	text := handles.BuildMintBackText(res)
	if len(m) == 0 && text == "" {
		return nil
	}
	if text != "" {
		// minted (announce) or invalid (correct). The invalid shape carries
		// instructions and any agent_id, but no session_id key.
		m[constants.MirrorInstructionsKey] = text
	} else {
		m[constants.MirrorInstructionsKey] = constants.MintBackConfirmed(names)
	}
	return m
}

// ShouldMirror gates the structured mirror on the output-injection registry:
// only tools whose declared outputSchema we extended get the mirror —
// except when no registry exists at all (rebuild failed), in which case the
// client cannot be validating against a schema we know about, so mirror.
func ShouldMirror(toolName string, reg *Registries) bool {
	if reg == nil {
		return true
	}
	return reg.OutputInjected[toolName]
}
