// Package mcpgo provides AgentCat tracking integration for mark3labs/mcp-go
// servers.
//
// It installs a tools/list hook that injects session/agent handles and the
// context parameter into advertised tool schemas, plus a tool handler
// middleware that resolves handles statelessly on every tools/call, mints session
// IDs back to the agent on the wire, and publishes one mcp:tools/call event
// per call to AgentCat.
package mcpgo

import (
	"context"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk/v2"
)

// Re-export core types so users don't need to import the core module directly.
type (
	UserIdentity     = agentcat.UserIdentity
	AgentCatInstance = agentcat.AgentCatInstance
	CustomEventData  = agentcat.CustomEventData
	ExporterConfig   = agentcat.ExporterConfig

	// Event is the published event as the RedactEvent hook sees it.
	Event = agentcat.Event
)

// MCPcatInstance is the former name of AgentCatInstance.
//
// Deprecated: use AgentCatInstance.
type MCPcatInstance = AgentCatInstance

// Options configures AgentCat tracking for mark3labs/mcp-go servers.
type Options struct {
	// DisableReportMissing, when true, prevents the automatic "get_more_tools"
	// tool from being registered. By default (false) the tool is added.
	DisableReportMissing bool

	// DisableToolCallContext, when true, prevents the "context" parameter from
	// being injected into existing tools. By default (false) it is added.
	DisableToolCallContext bool

	// DisableTracing, when true, prevents any events from being published to
	// AgentCat and suppresses handle injection: no session_id or agent_id
	// parameter is added to any tool and no mint-back is written to the wire,
	// because there is nothing left to correlate. The "context" parameter and
	// the get_more_tools tool still honor their own flags.
	DisableTracing bool

	// CustomContextDescription overrides the default description of the
	// injected "context" parameter. Use it to provide domain-specific guidance
	// to LLMs about what context they should provide. Only applies when tool
	// call context injection is enabled.
	CustomContextDescription string

	// EnableAgentTracking, when true, injects an "agent_id" string parameter
	// into tool input schemas so parallel agents working the same session can be
	// distinguished. The value is self-chosen by the agent. An omitted
	// agent_id NEVER rejects the call: the event simply publishes without
	// agent identity. Off by default.
	//
	// Injection is best-effort per tool, exactly like session_id and context: a
	// tool that already declares its own agent_id keeps it, and a tool whose
	// input schema is composed (oneOf/allOf/anyOf), unreadable, or absent is
	// left alone. Where it IS injected it is marked required in the
	// advertised schema.
	EnableAgentTracking bool

	// ResolveSessionID, when set, puts the SDK in hook mode: no session_id
	// parameter is injected anywhere and no session instructions are shown to
	// the agent — you own session correlation. The returned string is
	// deterministically derived (with the project ID) into a ses_ session ID,
	// so the same value always maps to the same session across processes,
	// restarts, and languages. Return "" or an error to mint a random session
	// silently for that call. The hook runs on every tool call; keep it
	// cheap.
	ResolveSessionID func(ctx context.Context, request mcp.CallToolRequest) (string, error)

	// Debug enables debug logging to ~/agentcat.log. When false, no logging occurs.
	Debug bool

	// Identify is called on every captured tool call to identify the actor.
	// The request argument is the *mcp.CallToolRequest that triggered the
	// event. The returned identity stamps that one event: there is no session
	// to merge into and no separate identify event, so nothing carries over
	// between calls. An identity whose UserID is empty names no actor and is
	// ignored. Return nil to skip identification for a call. The hook runs
	// inline on every call; keep it cheap (no network calls).
	Identify func(ctx context.Context, request any) *UserIdentity

	// EventTags is called on every auto-captured event to attach string
	// key-value tags. The request argument is the *mcp.CallToolRequest that
	// triggered the event — tool calls are the only events v2 captures. Tags
	// are validated client-side: keys must be at most 32 chars matching
	// [a-zA-Z0-9$_.:\- ], values at most 200 chars with no newlines, at most
	// 50 entries per event. Invalid entries are dropped with a warning logged.
	// If the callback panics or returns nil/empty, tags are omitted.
	EventTags func(ctx context.Context, request any) map[string]string

	// EventProperties is called on every auto-captured event to attach
	// arbitrary JSON metadata (no validation is applied). The request argument
	// is the *mcp.CallToolRequest that triggered the event. If the callback
	// panics or returns nil/empty, properties are omitted.
	EventProperties func(ctx context.Context, request any) map[string]any

	// RedactSensitiveInformation redacts sensitive data before sending to AgentCat.
	RedactSensitiveInformation func(text string) string

	// RedactEvent is the event-level redaction hook, invoked with the full
	// event (inspect ResourceName, EventType, Parameters, Response, etc.)
	// before it is published. Return a modified event, or nil to drop the
	// event entirely. Runs before RedactSensitiveInformation, so it sees
	// raw, unredacted values. The system-managed fields Id, SessionId,
	// ProjectId, EventType, and Timestamp cannot be changed. If the hook
	// returns an error or panics, the event is dropped and the error is
	// logged.
	RedactEvent func(event *Event) (*Event, error)

	// DisableDiagnostics disables AgentCat's internal SDK diagnostics. On by default;
	// also disable via the DISABLE_DIAGNOSTICS env var. ~/agentcat.log is unaffected.
	DisableDiagnostics bool

	// APIBaseURL overrides the default AgentCat API endpoint.
	// When empty, the SDK falls back to the AGENTCAT_API_URL environment
	// variable, then the legacy MCPCAT_API_URL environment variable, and then
	// to the built-in default (https://api.agentcat.com).
	APIBaseURL string

	// Exporters configure telemetry exporters that receive every captured
	// event in addition to (and independent of) the AgentCat API. Available
	// exporter types: "otlp", "datadog", "sentry", "posthog". When at least
	// one exporter is configured, projectID may be empty (telemetry-only
	// mode): events then go only to the exporters.
	Exporters map[string]ExporterConfig
}

// DefaultOptions returns a new Options with sensible defaults.
// All features are enabled by default (Disable* fields are false).
func DefaultOptions() *Options {
	return &Options{}
}

// Track attaches AgentCat tracking to the given MCPServer.
// It registers the server in the global registry, initializes the event
// publisher, and installs the tools/list schema injection hook, the tool-call
// middleware that resolves session handles and captures events, and the optional
// get_more_tools tool.
//
// projectID may be empty when at least one exporter is configured
// (telemetry-only mode): events are then sent only to the exporters and
// never to the AgentCat API.
//
// Track is idempotent per server: calling it twice on the same MCPServer
// installs nothing the second time and the first call's configuration stands.
//
// Track also declares AgentCat's own properties on the REGISTERED schemas of
// the tools it finds (and, on each tools/list, of any tool registered later).
// mcp-go validates a call's arguments against the registered input schema
// before any middleware can strip them, and a result against the registered
// output schema after the middleware has decorated it, so a server running
// WithInputSchemaValidation or WithOutputSchemaValidation would otherwise
// reject the very calls and results AgentCat's advertised schemas ask for.
// The declarations are optional properties only: `required` and
// `additionalProperties` are left exactly as the customer set them.
//
// Two visible consequences of that, neither of which changes tool behaviour:
// a tool that gains a declaration carries it as RawInputSchema /
// RawOutputSchema afterwards, so MCPServer.ListTools() reports it in the raw
// form rather than the structured InputSchema / OutputSchema fields. And the
// declaration is written directly onto the registered tool rather than
// re-registered, so it never emits notifications/tools/list_changed — a
// notification on every tools/list would drive any client that re-lists on it
// into a loop.
//
// Events are built and published synchronously on the request path — as they
// are in the officialsdk adapter, so the two behave alike — which means a slow
// Identify / EventTags / EventProperties callback adds latency to that one
// call. Keep them cheap. Handing the event to the publisher does not block,
// and nothing is left in flight at shutdown as a result.
//
// On success it returns a shutdown function that flushes pending events and
// releases resources. The shutdown function is idempotent and safe to call
// multiple times. On error it returns (nil, err).
func Track(mcpServer *server.MCPServer, projectID string, opts *Options) (func(context.Context) error, error) {
	if opts == nil {
		opts = DefaultOptions()
	}

	// The log singleton is created by the first emitted line with the debug
	// flag as it stands at that moment, and InitDiagnostics below emits the
	// first line. Enable-only: a failing or repeat Track must never disable
	// logging that another Track enabled.
	if opts.Debug {
		agentcat.SetDebug(true)
	}

	agentcat.InitDiagnostics(projectID, opts.DisableDiagnostics, "mcpgo",
		"github.com/mark3labs/mcp-go")

	if mcpServer == nil {
		agentcat.LogSetupFailed("server must not be nil")
		return nil, agentcat.ErrNilServer
	}
	if projectID == "" && len(opts.Exporters) == 0 {
		agentcat.LogSetupFailed("projectID must not be empty when no exporters are configured")
		return nil, agentcat.ErrEmptyProjectID
	}

	// Track is idempotent per server: a second call must not install a second
	// tool middleware, which would put two mint-back blocks on every wire
	// response. The first Track's configuration stands.
	if tracked := agentcat.GetInstance(mcpServer); tracked != nil {
		agentcat.LogSetupComplete(tracked.ProjectID, tracked.Options)
		return newShutdownFn(), nil
	}

	apiBaseURL := agentcat.ResolveAPIBaseURL(opts.APIBaseURL)

	coreOpts := &agentcat.Options{
		DisableReportMissing:       opts.DisableReportMissing,
		DisableToolCallContext:     opts.DisableToolCallContext,
		DisableTracing:             opts.DisableTracing,
		CustomContextDescription:   opts.CustomContextDescription,
		EnableAgentTracking:        opts.EnableAgentTracking,
		Debug:                      opts.Debug,
		RedactSensitiveInformation: opts.RedactSensitiveInformation,
		RedactEvent:                opts.RedactEvent,
		DisableDiagnostics:         opts.DisableDiagnostics,
		APIBaseURL:                 apiBaseURL,
		Exporters:                  opts.Exporters,
	}

	instance := &agentcat.AgentCatInstance{
		ProjectID: projectID,
		Options:   coreOpts,
	}
	agentcat.RegisterServer(mcpServer, instance)
	agentcat.SetDebug(opts.Debug)

	publishFn := agentcat.InitPublisher(opts.RedactSensitiveInformation, opts.RedactEvent, apiBaseURL, opts.Exporters)

	installTracking(mcpServer, instance, opts, publishFn)

	agentcat.LogSetupComplete(projectID, coreOpts)

	return newShutdownFn(), nil
}

// newShutdownFn returns an idempotent shutdown function that drains the
// publisher once, however many times (and from however many goroutines) it is
// called.
func newShutdownFn() func(context.Context) error {
	var once sync.Once
	return func(ctx context.Context) error {
		var err error
		once.Do(func() {
			err = agentcat.Shutdown(ctx)
		})
		return err
	}
}

// installTracking wires AgentCat onto the customer's server: the tools/list
// injection hook and the tool-call middleware. Tests drive it directly with a
// spy publisher so they exercise the same wiring Track installs.
//
// Hooks compose with whatever the customer registered at construction
// (server.WithHooks): GetHooks returns that same struct. AgentCat's hook
// closures carry no per-server state — they resolve the live server from the
// request context — so each Hooks value is instrumented exactly ONCE, no
// matter how many servers the customer attaches it to. Appending per Track()
// would grow the customer's hook slices forever in the per-request factory
// topology, and closures capturing the server would pin every dead request's
// server through the customer's long-lived Hooks value.
func installTracking(
	mcpServer *server.MCPServer,
	instance *agentcat.AgentCatInstance,
	opts *Options,
	publishFn func(*agentcat.Event),
) {
	hooks := mcpServer.GetHooks()
	if hooks == nil {
		hooks = &server.Hooks{}
		server.WithHooks(hooks)(mcpServer)
	}

	// What the context-dispatched hook closures resolve a live server to.
	instance.HookMode = opts.ResolveSessionID != nil
	capture := newCapturer(mcpServer, instance.ProjectID, opts, publishFn)
	registerCapturer(mcpServer, capture)

	instrumentHooksOnce(hooks, func() {
		registerListInjection(hooks)
		registerFailureHooks(hooks)
	})
	stashRebuild(instance, mcpServer)
	// The tool middleware is the capturer's ONLY strong owner — the side map
	// holds it weakly. Removing or gating this Use call would let GC take the
	// capturer and silently kill this server's failure hooks.
	mcpServer.Use(newToolMiddleware(capture))
	registerGetMoreToolsIfEnabled(mcpServer, instance.Options)

	// mcp-go validates a call's arguments against the REGISTERED input schema
	// before the middleware can strip them, and a result against the
	// REGISTERED output schema after the middleware has decorated it — so
	// both schemas have to declare AgentCat's properties before the first
	// request arrives.
	declareRegisteredSchemas(mcpServer, agentcat.BuildInjectConfig(instance.Options, opts.ResolveSessionID != nil))
}

// getMCPcat retrieves the AgentCatInstance associated with the given MCPServer.
// Returns nil if the server has not been registered via Track.
func getMCPcat(mcpServer *server.MCPServer) *agentcat.AgentCatInstance {
	return agentcat.GetInstance(mcpServer)
}

// unregisterServer removes the MCPServer from the global tracking registry.
func unregisterServer(mcpServer *server.MCPServer) {
	agentcat.UnregisterServer(mcpServer)
}

// Shutdown gracefully shuts down the global event publisher.
// The provided context controls the shutdown deadline.
func Shutdown(ctx context.Context) error {
	return agentcat.Shutdown(ctx)
}

// PublishCustomEvent publishes a customer-defined event to AgentCat.
// serverOrSessionID is either a tracked *server.MCPServer (the event
// publishes without a session unless data.SessionID is set) or a session ID string
// used verbatim; projectID is required. See agentcat.PublishCustomEvent
// for details.
func PublishCustomEvent(serverOrSessionID any, projectID string, data *CustomEventData) error {
	return agentcat.PublishCustomEvent(serverOrSessionID, projectID, data)
}
