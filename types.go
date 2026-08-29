package agentcat

import (
	"slices"

	"go.agentcat.com/sdk/v2/internal/constants"
	"go.agentcat.com/sdk/v2/internal/core"
	"go.agentcat.com/sdk/v2/internal/inject"
)

type (
	// UserIdentity names the actor behind a call, as an adapter's Identify
	// callback returned it.
	UserIdentity = core.UserIdentity

	// Options is the library-neutral tracked configuration. Each adapter maps
	// its own public Options onto this once, at Track time.
	Options = core.Options

	// RedactFunc redacts sensitive text before an event is published.
	RedactFunc = core.RedactFunc

	// RedactEventFunc is the event-level redaction hook; returning nil drops
	// the event.
	RedactEventFunc = core.RedactEventFunc

	// Exporter forwards published events to an external telemetry system.
	Exporter = core.Exporter

	// ExporterConfig configures one telemetry exporter.
	ExporterConfig = core.ExporterConfig

	// Event is a published event, as the RedactEvent hook sees it.
	Event = core.Event

	// AgentCatInstance is the per-server tracked state the registry holds:
	// project, options, injection registries, and the rebuild hook.
	AgentCatInstance = core.AgentCatInstance

	// CustomEventData describes a customer-defined event.
	CustomEventData = core.CustomEventData
)

// MCPcatInstance is the former name of AgentCatInstance.
//
// Deprecated: use AgentCatInstance.
type MCPcatInstance = AgentCatInstance

// IDPrefix is the leading segment of an AgentCat-generated ID.
type IDPrefix = core.IDPrefix

const (
	PrefixSession IDPrefix = core.PrefixSession
	PrefixEvent   IDPrefix = core.PrefixEvent

	// PrefixAgent is reserved across every AgentCat SDK. Nothing mints it —
	// agent IDs are self-chosen by the agent — but the prefix must never be
	// reused for anything else.
	PrefixAgent IDPrefix = core.PrefixAgent
)

// DefaultOptions returns the library-neutral options with every feature at
// its default (all Disable* flags false).
func DefaultOptions() Options {
	return core.DefaultOptions()
}

// Wire keys, injected parameter names, tags, and agent-facing copy shared
// with the adapters (which cannot import internal/ across module boundaries).
//
// Every agent-facing string here is byte-identical to the TypeScript SDK and
// is defined exactly once, in internal/constants. Never retype one of these
// values as a literal — not in an adapter, not in a test.
const (
	// ParamSessionID, ParamAgentID and ParamContext are the three parameter
	// names this SDK may inject into a tool's input schema.
	ParamSessionID = constants.ParamSessionID
	ParamAgentID   = constants.ParamAgentID
	ParamContext   = constants.ParamContext

	// MCPSessionKey is the structuredContent member carrying the handle
	// mirror, and the property declared on extended output schemas.
	MCPSessionKey = constants.MCPSessionKey

	// MetaClientInfoKey and MetaProtocolVersionKey are the reserved _meta keys
	// a 2026-07-28 client stamps on every request.
	MetaClientInfoKey      = constants.MetaClientInfoKey
	MetaProtocolVersionKey = constants.MetaProtocolVersionKey

	// The SDK-owned event tags. These are set on every published event and
	// are exempt from the customer tag cap.
	TagSessionSource   = constants.TagSessionSource
	TagAgentID         = constants.TagAgentID
	TagAgentSource     = constants.TagAgentSource
	TagProtocolVersion = constants.TagProtocolVersion
	TagMRTR            = constants.TagMRTR

	// MRTR tag values: an intermediate round that asked the client for input,
	// and the continuation round that answered one.
	MRTRInputRequired = constants.MRTRInputRequired
	MRTRContinuation  = constants.MRTRContinuation

	// The optional get_more_tools tool's name and agent-facing copy.
	GetMoreToolsName               = constants.GetMoreToolsName
	GetMoreToolsDescription        = constants.GetMoreToolsDescription
	GetMoreToolsContextDescription = constants.GetMoreToolsContextDescription
	GetMoreToolsResponseText       = constants.GetMoreToolsResponseText
)

type (
	// SchemaObject is a JSON object that preserves key order and the raw
	// bytes of every value, so a customer's schema round-trips byte for byte
	// through injection.
	SchemaObject = inject.SchemaObject

	// NormalizedTool is the engine's library-neutral view of one tool. An
	// adapter builds these from its library's tools and writes the mutated
	// schemas back onto its own copies afterwards.
	NormalizedTool = inject.NormalizedTool

	// InjectConfig selects what the engine injects. Build it with
	// BuildInjectConfig; never assemble one by hand.
	InjectConfig = inject.Config

	// Registries record exactly what the engine injected per tool. They drive
	// argument stripping (StripToolArguments) and the mirror gate
	// (ShouldMirror), so an adapter must keep them alive for every tool that
	// has ever been listed — see AgentCatInstance.MergeRegistries.
	Registries = inject.Registries
)

// NewSchemaObject returns an empty ordered JSON object.
func NewSchemaObject() *SchemaObject { return inject.NewSchemaObject() }

// ParseSchemaObject parses raw as a JSON object, preserving key order. An
// error means the document is not a JSON object; the adapter must then treat
// the schema as opaque and advertise it untouched rather than replace it.
func ParseSchemaObject(raw []byte) (*SchemaObject, error) {
	return inject.ParseSchemaObject(raw)
}

// BuildInjectedTools runs the pure injection pipeline: it returns the
// advertised tools and the registries describing exactly what it injected.
// Pure, deterministic, and idempotent — it never mutates its inputs and never
// fails a tool list; a tool it cannot safely touch passes through with an
// empty registry entry.
func BuildInjectedTools(cfg InjectConfig, tools []NormalizedTool) ([]NormalizedTool, *Registries) {
	return inject.Build(cfg, tools)
}

// BuildInjectConfig derives the pure pipeline config from tracked options.
// Deterministic: rebuild-on-demand depends on Build(cfg, tools) producing
// identical registries on every server instance. Adapters pass
// hookMode = (their Options.ResolveSessionID != nil).
func BuildInjectConfig(opts *Options, hookMode bool) InjectConfig {
	return InjectConfig{
		InjectHandles:      !opts.DisableTracing,
		HookMode:           hookMode,
		AgentTracking:      opts.EnableAgentTracking,
		InjectContext:      !opts.DisableToolCallContext,
		ContextDescription: ResolveContextDescription(opts.CustomContextDescription),
	}
}

// ReportSessionParamCollisions logs, at most once per tool per tracked server,
// each customer-declared session_id parameter the engine refused to overwrite.
// Adapters call it after storing the registries a tools/list produced.
//
// A session_id collision is an ERROR rather than a warning because it costs
// the customer correlation outright: every call to that tool publishes without
// a session and cannot be grouped with anything else. agent_id and context
// collisions stay warnings — they lose an attribute, not the thread.
//
// Logging only. It reads the registries the pure engine produced and never
// affects them, so inject.Build stays deterministic.
func ReportSessionParamCollisions(instance *AgentCatInstance, reg *Registries) {
	if instance == nil || reg == nil {
		return
	}
	for toolName, owned := range reg.CustomerOwnedParams {
		if !slices.Contains(owned, ParamSessionID) {
			continue
		}
		if !instance.ClaimCollisionReport(toolName) {
			continue
		}
		logError("inject: tool %q already declares a %q parameter. AgentCat will not inject "+
			"its own, and calls to this tool are published without a session, so they cannot "+
			"be correlated. Your parameter is untouched and still reaches your handler. If you "+
			"already manage sessions, set Options.ResolveSessionID when you call Track — "+
			"AgentCat will derive its session from your identifier and stop injecting %s entirely.",
			toolName, ParamSessionID, ParamSessionID)
	}
}

// injectSessionParamIsOurs is the engine predicate behind SessionParamIsOurs
// (declared here because mcpcat.go does not import internal/inject).
var injectSessionParamIsOurs = inject.SessionParamIsOurs

// MirrorInput describes what the structured mirror may name for one call.
type MirrorInput = inject.MirrorInput

// StripToolArguments returns a COPY of args with only the parameters the
// registries say this SDK injected for this tool removed. The customer's
// request object is never mutated, and the raw arguments still go on the
// published event. With no registries at all it falls back to removing the
// three injectable names heuristically.
func StripToolArguments(toolName string, args map[string]any, reg *Registries) map[string]any {
	return inject.Strip(toolName, args, reg)
}

// BuildHandleMirror assembles the mcp_session value mirrored into a
// response's structuredContent. Returns nil when there is nothing the agent
// could echo back.
func BuildHandleMirror(in MirrorInput) map[string]any { return inject.BuildMirror(in) }

// ShouldMirror reports whether this tool's declared output schema was
// extended to allow the mirror. Writing the mirror into a response whose
// schema does not declare it would fail the customer's own output validation.
func ShouldMirror(toolName string, reg *Registries) bool {
	return inject.ShouldMirror(toolName, reg)
}
