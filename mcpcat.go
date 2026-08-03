// Package agentcat is the shared integration surface of the AgentCat Go SDK.
//
// # Which package do I import?
//
// If you are instrumenting an MCP server, you want an ADAPTER, not this
// package:
//
//	go.agentcat.com/sdk/mcpgo/v2        // github.com/mark3labs/mcp-go servers
//	go.agentcat.com/sdk/officialsdk/v2  // github.com/modelcontextprotocol/go-sdk servers
//
// Each adapter exposes a single Track entry point plus its own Options, and
// re-exports every type an end user needs (UserIdentity, Event, ExporterConfig,
// CustomEventData), so a customer's server never imports this package
// directly.
//
// # What is in here
//
// This package holds everything the two adapters share and neither may
// duplicate: the pure schema-injection engine, the stateless session/agent handle
// primitives, the agent-facing copy (byte-identical across every AgentCat
// SDK), event construction, the server registry, and the publisher. The
// adapters live in their own Go modules, so they cannot reach this module's
// internal/ packages — everything they need is re-exported here, and nothing
// else is exported.
//
// The API here is stable for the adapters that ship with this SDK. It is not
// a general-purpose API: it may change in a minor release if both adapters
// change with it.
//
// # How the pieces fit together
//
// On tools/list an adapter normalises its library's tools into
// []NormalizedTool, calls BuildInjectedTools, writes the mutated schemas back
// onto its own copies, and folds the returned Registries into the server's
// AgentCatInstance. On tools/call it resolves handles with ResolveSessionHandle,
// removes what the registries say it injected with StripToolArguments,
// dispatches, decorates a COPY of the response with BuildMintBackText and
// BuildHandleMirror (the wire only — the published event always carries the
// customer's raw request and undecorated response), and publishes one event
// built by NewToolCallEvent.
package agentcat

import (
	"context"
	"errors"
	"os"

	"go.agentcat.com/sdk/v2/internal/core"
	"go.agentcat.com/sdk/v2/internal/diagnostics"
	"go.agentcat.com/sdk/v2/internal/event"
	"go.agentcat.com/sdk/v2/internal/handles"
	"go.agentcat.com/sdk/v2/internal/logging"
	"go.agentcat.com/sdk/v2/internal/publisher"
	"go.agentcat.com/sdk/v2/internal/redaction"
	"go.agentcat.com/sdk/v2/internal/registry"
	"go.agentcat.com/sdk/v2/internal/validation"
)

// Sentinel errors for Track validation.
var (
	ErrNilServer      = errors.New("agentcat: server must not be nil")
	ErrEmptyProjectID = errors.New("agentcat: projectID must not be empty")
)

// --- Integration API (used by mcpgo/ and officialsdk/ modules) ---

// RegisterServer stores the AgentCat instance for a given server in the global registry.
func RegisterServer[T any](server *T, instance *AgentCatInstance) {
	registry.Register(server, instance)
}

// GetInstance retrieves the AgentCat instance for a given server from the global registry.
func GetInstance(server any) *AgentCatInstance {
	return registry.Get(server)
}

// UnregisterServer removes a server from the global registry.
func UnregisterServer(server any) {
	registry.Unregister(server)
}

// InitPublisher initializes the global event publisher and returns a publish function.
// The returned function can be called to publish events asynchronously.
// If apiBaseURL is empty, the default AgentCat API URL is used. When exporter
// configs are provided, every published event is also fanned out to the
// configured telemetry exporters, independently of the AgentCat API send.
func InitPublisher(redactFn RedactFunc, redactEventFn RedactEventFunc, apiBaseURL string, exporterConfigs map[string]ExporterConfig) func(evt *Event) {
	pub := publisher.GetOrInit(redactFn, redactEventFn, apiBaseURL, exporterConfigs)
	return func(evt *Event) {
		if evt != nil {
			pub.Publish(evt)
		}
	}
}

// Shutdown gracefully shuts down the global event publisher.
// This should be called when the application is shutting down to ensure
// all queued events are published before exit. The provided context controls
// the shutdown deadline; if no deadline is set, a default 5-second timeout
// is applied.
func Shutdown(ctx context.Context) error {
	err := publisher.ShutdownGlobal(ctx)
	diagnostics.Flush()
	return err
}

// SetDebug enables or disables debug logging globally.
func SetDebug(debug bool) {
	logging.SetGlobalDebug(debug)
}

// LogRecoveredPanic logs a panic recovered inside SDK capture code (hooks,
// middleware, capture goroutines). Integration API for adapter modules:
// analytics failures must never crash the customer's server, so capture code
// recovers, calls this, and drops the event.
func LogRecoveredPanic(where string, recovered any) {
	logging.New().Errorf("Recovered panic in %s: %v - event dropped", where, recovered)
}

// ValidateTags validates customer-supplied event tags against AgentCat's
// client-side constraints, dropping (and warn-logging) invalid entries.
// Returns nil when no valid entries remain.
func ValidateTags(tags map[string]string) map[string]string {
	return validation.ValidateTags(tags)
}

// GetDependencyVersion returns the version of the given module from build info,
// or "dev" if the module is not found.
func GetDependencyVersion(modulePath string) string {
	return core.GetDependencyVersion(modulePath)
}

// NewEventID generates a new unique event ID with the AgentCat prefix.
func NewEventID() string {
	return event.NewEventID()
}

// RedactEvent applies the redaction function to sensitive fields in the event.
func RedactEvent(evt *Event, redactFn RedactFunc) error {
	return redaction.RedactEvent(evt, redactFn)
}

// ConvertToMap converts any value to map[string]any or []any via JSON round-trip.
func ConvertToMap(v any) any {
	return event.ConvertToMap(v)
}

// ResolveAPIBaseURL returns the API base URL to use, applying the priority:
// code option > AGENTCAT_API_URL env var > MCPCAT_API_URL env var (legacy
// fallback) > empty string (publisher uses default).
func ResolveAPIBaseURL(optionURL string) string {
	if optionURL != "" {
		return optionURL
	}
	if envURL := os.Getenv("AGENTCAT_API_URL"); envURL != "" {
		return envURL
	}
	if envURL := os.Getenv("MCPCAT_API_URL"); envURL != "" {
		return envURL
	}
	return ""
}

// Ptr returns a pointer to the given value. Convenience helper for integration modules.
func Ptr[T any](v T) *T {
	return core.Ptr(v)
}

// ── v2 handle primitives (integration API for adapters) ─────────────────────

type (
	// SessionSource records how a call's session ID was obtained: echoed by the
	// agent, minted by this SDK, or derived from the customer's hook.
	SessionSource = handles.SessionSource

	// SessionResolution is the outcome of one call's stateless session resolution.
	SessionResolution = handles.SessionResolution
)

const (
	// SessionSourceSupplied: the agent echoed a session_id argument.
	SessionSourceSupplied = handles.SessionSourceSupplied
	// SessionSourceMinted: this SDK issued a fresh session for this call.
	SessionSourceMinted = handles.SessionSourceMinted
	// SessionSourceHook: derived from the customer's ResolveSessionID callback.
	SessionSourceHook = handles.SessionSourceHook
	// SessionSourceInvalid: the agent sent a session_id this server never
	// issued. The call publishes sessionless and the agent is told to re-send
	// the real one.
	SessionSourceInvalid = handles.SessionSourceInvalid
	// SessionSourceForeign: the customer's own tool declares session_id, so
	// AgentCat never injected one there. The call publishes sessionless and
	// nothing is said to the agent about a parameter that is not ours.
	SessionSourceForeign = handles.SessionSourceForeign
)

// IsValidSessionID reports whether value is a session ID this SDK issued —
// the ses_ prefix plus a 27-character base62 KSUID. Anything else was
// invented by the agent or belongs to someone else, and is never adopted into
// Event.SessionId, which both redaction hooks are exempt from.
func IsValidSessionID(value string) bool { return handles.IsValidSessionID(value) }

// MintSessionID returns a fresh random ses_-prefixed session ID.
func MintSessionID() string { return handles.MintSessionID() }

// DeriveSessionID maps a customer correlation ID (plus the project ID) onto a
// stable ses_ session ID. Deterministic across processes, restarts, and every
// AgentCat SDK: the same input always yields the same session.
func DeriveSessionID(customerID, projectID string) string {
	return handles.DeriveSessionID(customerID, projectID)
}

// ExtractHandle returns args[name] when it is a string with non-blank
// content, VERBATIM (never trimmed or reformatted). It is shape-agnostic
// because it is shared with agent_id, which this SDK never validates;
// ResolveSessionHandle applies IsValidSessionID to the session handle.
func ExtractHandle(args map[string]any, name string) (string, bool) {
	return handles.ExtractHandle(args, name)
}

// ResolveSessionHandle resolves the session for one call, statelessly. hook is
// non-nil in hook mode (the adapter closes over the customer's ResolveSessionID),
// in which case the supplied arguments are ignored entirely. Never fails: a
// blank, errored, or panicking hook mints a session silently.
//
// sessionParamIsOurs comes from SessionParamIsOurs and must be computed from
// registries the adapter has already loaded — resolve AFTER loading them.
// Passing true for a tool the customer owns would adopt their value.
func ResolveSessionHandle(args map[string]any, hook func() (string, error), projectID string, sessionParamIsOurs bool) SessionResolution {
	return handles.ResolveSessionHandle(args, hook, projectID, sessionParamIsOurs)
}

// ClampAgentID prepares a supplied agent_id for the SDK tag channel, which
// bypasses customer tag validation: newlines become spaces and the value is
// truncated to 200 bytes on a rune boundary.
func ClampAgentID(v string) string { return handles.ClampAgentID(v) }

// BuildMintBackText renders the trailing [MCP INSTRUCTIONS] content block for
// one call, or "" when there is nothing to say. It is the single decision
// point for whether a call announces anything: minted announces the new
// handle, invalid corrects the agent without issuing a replacement, and hook,
// foreign and supplied say nothing. Adapters must not re-derive it.
func BuildMintBackText(res SessionResolution) string { return handles.BuildMintBackText(res) }

// SessionParamIsOurs reports whether the session_id argument on a call to
// toolName is AgentCat's to read. A tool absent from the registries counts as
// ours, so a call arriving before any tools/list is still validated.
func SessionParamIsOurs(toolName string, reg *Registries) bool {
	return injectSessionParamIsOurs(toolName, reg)
}

// EventContext is the per-request identity and handle state an adapter
// resolves before building an event.
type EventContext = event.EventContext

// NewToolCallEvent builds the single mcp:tools/call event for one call.
// duration is nil when nothing of this SDK's timed the call. Returns nil when
// the context is unusable.
func NewToolCallEvent(ec *EventContext, duration *int32, isError bool, errorDetails error) *Event {
	return event.NewToolCallEvent(ec, duration, isError, errorDetails)
}

// ApplySDKTags stamps the SDK-owned tags (session source, agent ID and its
// source, protocol version, MRTR) onto an event. Call it AFTER the customer's
// own tags: the SDK's win, and they are exempt from the customer tag cap.
func ApplySDKTags(evt *Event, ec *EventContext) { event.ApplySDKTags(evt, ec) }
