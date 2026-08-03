package core

import (
	"context"
	"sync"
	"sync/atomic"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/v2/internal/constants"
	"go.agentcat.com/sdk/v2/internal/inject"
)

// UserIdentity represents a user identity returned by the identify function
type UserIdentity struct {
	// UserID is the unique identifier for the user
	UserID string `json:"userId"`

	// UserName is the optional user name
	UserName string `json:"userName,omitempty"`

	// UserData contains additional user data as key-value pairs
	UserData map[string]any `json:"userData,omitempty"`
}

// RedactFunc redacts sensitive information from text before it is sent upstream
type RedactFunc func(text string) string

// RedactEventFunc is the event-level redaction hook. It receives the full
// event before it is published and returns a modified event, or nil to drop
// the event entirely. A non-nil error also drops the event and is logged.
type RedactEventFunc func(event *Event) (*Event, error)

// Exporter is an interface for telemetry exporters that forward events
// to external systems.
type Exporter interface {
	Export(event *Event) error
}

// ExporterConfig configures a telemetry exporter. Type selects the exporter
// implementation; the remaining fields apply only to the exporter noted in
// their comment. Unknown types are skipped with a warning logged.
type ExporterConfig struct {
	// Type selects the exporter: "otlp", "datadog", "sentry", or "posthog".
	Type string `json:"type"`

	// Endpoint is the OTLP collector endpoint (otlp, required). The /v1/traces
	// path is appended automatically when missing.
	Endpoint string `json:"endpoint,omitempty"`

	// Headers are additional HTTP headers sent with each request (otlp).
	Headers map[string]string `json:"headers,omitempty"`

	// APIKey authenticates with the provider (datadog and posthog, required).
	APIKey string `json:"apiKey,omitempty"`

	// Site is the Datadog site, e.g. "datadoghq.com" or "datadoghq.eu"
	// (datadog, required).
	Site string `json:"site,omitempty"`

	// Service is the service name reported to Datadog (datadog, required).
	Service string `json:"service,omitempty"`

	// Env is the deployment environment tag (datadog, optional).
	Env string `json:"env,omitempty"`

	// DSN is the Sentry DSN (sentry, required).
	DSN string `json:"dsn,omitempty"`

	// Environment is the Sentry environment tag (sentry, optional).
	Environment string `json:"environment,omitempty"`

	// Release is the Sentry release tag (sentry, optional).
	Release string `json:"release,omitempty"`

	// EnableTracing also sends Sentry transactions for performance monitoring
	// (sentry; default false — logs and error events only).
	EnableTracing bool `json:"enableTracing,omitempty"`

	// Host is the PostHog instance URL (posthog; default
	// https://us.i.posthog.com, supports self-hosted and EU region).
	Host string `json:"host,omitempty"`

	// EnableAITracing emits $ai_span events for tool calls alongside regular
	// capture events, integrating with PostHog's AI observability views
	// (posthog; default false).
	EnableAITracing bool `json:"enableAITracing,omitempty"`
}

// Options configures the AgentCat tracking behavior.
type Options struct {
	// DisableReportMissing, when true, prevents the automatic "get_more_tools"
	// tool from being registered. By default (false) the tool is added so LLMs
	// can report missing functionality.
	DisableReportMissing bool

	// DisableToolCallContext, when true, prevents the "context" parameter from
	// being injected into existing tools. By default (false) the parameter is
	// added to capture user intent.
	DisableToolCallContext bool

	// EnableAgentTracking, when true, injects an "agent_id" string parameter
	// into tool input schemas so parallel agents working the same session can be
	// distinguished. The value is self-chosen by the agent; an omitted
	// agent_id NEVER rejects the call. Off by default.
	//
	// Injection is best-effort per tool, exactly like session_id and context: a
	// tool that already declares its own agent_id keeps it, and a tool whose
	// input schema is composed (oneOf/allOf/anyOf), unreadable, or absent is
	// left alone. Where it IS injected it is marked required in the
	// advertised schema.
	EnableAgentTracking bool

	// DisableTracing, when true, prevents any events from being published to
	// the AgentCat API and suppresses handle injection (BuildInjectConfig maps
	// it to InjectHandles): no session_id or agent_id parameter is added to any
	// tool and no mint-back is written to the wire. The "context" parameter and
	// the get_more_tools tool still honor their own flags.
	DisableTracing bool

	// CustomContextDescription overrides the default description of the
	// injected "context" parameter. Only applies when tool call context
	// injection is enabled.
	CustomContextDescription string

	// Debug enables debug logging to ~/agentcat.log. When false, no logging occurs.
	Debug bool

	// RedactSensitiveInformation redacts sensitive data before sending to AgentCat.
	RedactSensitiveInformation RedactFunc

	// RedactEvent is the event-level redaction hook, invoked with the full
	// event (inspect ResourceName, EventType, Parameters, Response, etc.)
	// before it is published. Return a modified event, or nil to drop the
	// event entirely. Runs before RedactSensitiveInformation, so it sees
	// raw, unredacted values. The system-managed fields Id, SessionId,
	// ProjectId, EventType, and Timestamp cannot be changed. If the hook
	// returns an error or panics, the event is dropped and the error is
	// logged.
	RedactEvent RedactEventFunc

	// Exporters configure telemetry exporters to send events to external systems.
	// Available exporters: otlp, datadog, sentry, posthog.
	Exporters map[string]ExporterConfig

	// DisableDiagnostics disables AgentCat's internal SDK diagnostics (anonymous
	// operational error/setup reporting used to detect SDK setup failures).
	// On by default; also disable via the DISABLE_DIAGNOSTICS env var.
	// Local ~/agentcat.log logging is unaffected.
	DisableDiagnostics bool

	// APIBaseURL overrides the default AgentCat API endpoint.
	// When empty, the SDK falls back to the AGENTCAT_API_URL environment
	// variable, then the legacy MCPCAT_API_URL environment variable, and then
	// to the built-in default (https://api.agentcat.com).
	APIBaseURL string
}

// Event wraps the API publish request and leaves room for SDK-specific fields.
type Event struct {
	agentcatapi.PublishEventRequest
	// SDK-specific fields can be added here if needed in the future. They should
	// use `json:"-"` to avoid serialization to the API.
}

// IDPrefix represents prefixes for AgentCat-generated IDs.
type IDPrefix string

// The prefix strings live in internal/constants, the single source shared
// with the pure engine and pinned byte-for-byte against the other SDKs.
const (
	// PrefixSession is the prefix for session IDs. It predates the rename
	// away from task_id and is unchanged by it, so dashboards, saved queries
	// and exporters that key on ses_ are unaffected.
	PrefixSession IDPrefix = constants.PrefixSession

	// PrefixEvent is the prefix for event IDs.
	PrefixEvent IDPrefix = constants.PrefixEvent

	// PrefixAgent is reserved and never minted; see internal/constants.
	PrefixAgent IDPrefix = constants.PrefixAgent
)

// AgentCatInstance represents the tracking configuration stored in the registry.
//
// Nothing here may hold a STRONG reference back to the server it describes.
// The registry keys entries by pointer address and releases them through
// runtime.AddCleanup, so any strong path from this value to the server would
// keep the server reachable from a package-level map forever and the cleanup
// would never fire — leaking one entry per Track() in the per-request factory
// topology. (This is why the v1 ServerRef field was removed in v2, and why
// RebuildTools closes over the server weakly.)
type AgentCatInstance struct {
	ProjectID string
	Options   *Options

	// Registries hold the injected-params and output-injection registries
	// produced by the most recent tools/list (or rebuild) for this server.
	// Nil until the first list; rebuilt on demand on the call path.
	Registries atomic.Pointer[inject.Registries]

	// RebuildTools re-fetches the server's ORIGINAL (uninjected) tool list
	// so the call path can rebuild registries on instances that never
	// served tools/list (per-request factory topology). session is the
	// adapter's in-flight session object. Set by the adapter at Track time.
	RebuildTools func(ctx context.Context, session any) ([]inject.NormalizedTool, error)

	// reportedCollisions names the tools whose session_id collision has already
	// been reported, so the error appears once per tool rather than on every
	// tools/list. Strings only — nothing reachable from this value may
	// reference the server (see the type comment above), and a set of tool
	// names cannot.
	collisionsMu       sync.Mutex
	reportedCollisions map[string]struct{}
}

// ClaimCollisionReport reports whether toolName's session_id collision has not
// yet been reported for this instance, marking it reported. It exists so the
// error carries remediation once rather than on every tools/list.
//
// In the per-request factory topology a fresh instance per request makes this
// a no-op and the error repeats per request. That matches the TypeScript SDK,
// and it is the right outcome anyway: a collision that persists across every
// request is a persistent misconfiguration worth saying every time.
func (i *AgentCatInstance) ClaimCollisionReport(toolName string) bool {
	i.collisionsMu.Lock()
	defer i.collisionsMu.Unlock()
	if _, seen := i.reportedCollisions[toolName]; seen {
		return false
	}
	if i.reportedCollisions == nil {
		i.reportedCollisions = make(map[string]struct{})
	}
	i.reportedCollisions[toolName] = struct{}{}
	return true
}

// MergeRegistries folds one list's registries into the instance's, and
// returns the merged value the call path should use.
//
// Adapters must call this rather than Registries.Store: a single tools/list
// describes only the tools that list returned (pagination, tool filters and
// session-scoped tools all narrow it), and a plain store would drop every
// tool the last list did not name — which silently stops argument stripping
// for them. The compare-and-swap loop keeps two concurrent lists on one
// instance from losing each other's entries.
func (i *AgentCatInstance) MergeRegistries(reg *inject.Registries) *inject.Registries {
	if reg == nil {
		return i.Registries.Load()
	}
	for {
		prev := i.Registries.Load()
		merged := inject.MergeRegistries(prev, reg)
		if i.Registries.CompareAndSwap(prev, merged) {
			return merged
		}
	}
}

// MCPcatInstance is the former name of AgentCatInstance.
//
// Deprecated: use AgentCatInstance.
type MCPcatInstance = AgentCatInstance

// CustomEventData describes a customer-defined event published via
// PublishCustomEvent.
type CustomEventData struct {
	// SessionID attributes this event to a session (ses_ handle). Takes precedence
	// over a string first argument to PublishCustomEvent. When empty and the
	// target is a tracked server, the event publishes without a session.
	SessionID string

	// ResourceName names the resource or action this event represents.
	ResourceName string

	// Parameters are arbitrary request data to attach to the event.
	Parameters map[string]any

	// Response is arbitrary response data to attach to the event.
	Response map[string]any

	// Message describes why the event occurred (sent as user_intent).
	Message string

	// Duration of the operation in milliseconds.
	Duration *int32

	// IsError marks the event as an error event.
	IsError bool

	// Error holds error details when IsError is true.
	Error error

	// Tags are custom string key-value pairs, validated client-side
	// (same constraints as the EventTags callback).
	Tags map[string]string

	// Properties are arbitrary JSON metadata attached to the event.
	Properties map[string]any
}

// DefaultOptions returns the default options for tracking.
// All features are enabled by default (Disable* fields are false).
func DefaultOptions() Options {
	return Options{}
}

// Ptr returns a pointer to the given value.
func Ptr[T any](v T) *T {
	return &v
}
