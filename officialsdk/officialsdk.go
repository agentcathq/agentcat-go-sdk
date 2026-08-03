// Package officialsdk provides AgentCat tracking integration for the official
// Go MCP SDK (github.com/modelcontextprotocol/go-sdk).
//
// It installs receiving middleware on an mcp.Server that injects session/agent
// handles and the context parameter into advertised tool schemas, resolves
// handles statelessly on every tools/call, mints session IDs back to the agent
// on the wire, and publishes one mcp:tools/call event per call to AgentCat.
package officialsdk

import (
	"context"
	"reflect"
	"sync"
	"unsafe"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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

// Options configures AgentCat tracking for the official Go MCP SDK.
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
	ResolveSessionID func(ctx context.Context, req mcp.Request) (string, error)

	// Debug enables debug logging to ~/agentcat.log. When false, no logging occurs.
	Debug bool

	// Identify is called on every captured tool call to identify the actor.
	// The request argument is the *mcp.CallToolRequest that triggered the
	// event. The returned identity stamps that one event: there is no session
	// to merge into and no separate identify event, so nothing carries over
	// between calls. An identity whose UserID is empty names no actor and is
	// ignored. Return nil to skip identification for a call. The hook runs
	// inline on every call; keep it cheap (no network calls).
	//
	// ctx is the LIVE request context, so it is already done if the client
	// cancelled the call or disconnected. A callback doing context-aware I/O
	// will fail in that case and the event publishes without an identity —
	// deliberately: capture runs on the request's own goroutine, and letting a
	// callback outlive the cancelled request would hold that goroutine open.
	// Read what you need from ctx rather than making calls with it.
	Identify func(ctx context.Context, request mcp.Request) *UserIdentity

	// EventTags is called on every auto-captured event to attach string
	// key-value tags. The request argument is the *mcp.CallToolRequest that
	// triggered the event — tool calls are the only events v2 captures. Tags
	// are validated client-side: keys must be at most 32 chars matching
	// [a-zA-Z0-9$_.:\- ], values at most 200 chars with no newlines, at most
	// 50 entries per event. Invalid entries are dropped with a warning logged.
	// If the callback panics or returns nil/empty, tags are omitted.
	//
	// ctx is the LIVE request context and carries the same cancellation
	// caveat as Identify.
	EventTags func(ctx context.Context, request mcp.Request) map[string]string

	// EventProperties is called on every auto-captured event to attach
	// arbitrary JSON metadata (no validation is applied). The request argument
	// is the *mcp.CallToolRequest that triggered the event. If the callback
	// panics or returns nil/empty, properties are omitted.
	//
	// ctx is the LIVE request context and carries the same cancellation
	// caveat as Identify.
	EventProperties func(ctx context.Context, request mcp.Request) map[string]any

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

// Track attaches AgentCat tracking middleware to the given mcp.Server.
// It registers the server in the global registry, initializes the event
// publisher, and installs receiving middleware for request timing, event
// capture, context parameter injection, and the optional get_more_tools tool.
//
// projectID may be empty when at least one exporter is configured
// (telemetry-only mode): events are then sent only to the exporters and
// never to the AgentCat API.
//
// Track is idempotent per server: calling it twice on the same mcp.Server
// installs nothing the second time and the first call's configuration stands.
//
// Events are built and published synchronously, on the tool call's own
// goroutine, exactly as the mcpgo adapter does it — so a slow Identify /
// EventTags / EventProperties callback adds latency to that one call. Keep
// them cheap. Handing the event to the publisher does not block: the publisher
// owns the only queue in the path, and drops with a warning if it overflows.
//
// On success it returns a shutdown function that flushes pending events and
// releases resources. The shutdown function is idempotent and safe to call
// multiple times. On error it returns (nil, err).
func Track(mcpServer *mcp.Server, projectID string, opts *Options) (func(context.Context) error, error) {
	if opts == nil {
		opts = DefaultOptions()
	}

	agentcat.InitDiagnostics(projectID, opts.DisableDiagnostics, "officialsdk",
		"github.com/modelcontextprotocol/go-sdk")

	if mcpServer == nil {
		agentcat.LogSetupFailed("server must not be nil")
		return nil, agentcat.ErrNilServer
	}
	if projectID == "" && len(opts.Exporters) == 0 {
		agentcat.LogSetupFailed("projectID must not be empty when no exporters are configured")
		return nil, agentcat.ErrEmptyProjectID
	}

	// Track is idempotent per server: a second call must not install a second
	// receiving middleware. Two would put two mint-back blocks on every wire
	// response and publish two events per call — and worse, the second one's
	// rebuild stash would capture the FIRST AgentCat middleware as its inner
	// handler, so a rebuild would return already-injected tools, the engine
	// would re-assert rather than inject, and argument stripping would stop
	// entirely. The first Track's configuration stands.
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

	// The server's Implementation, read once at Track() time and stamped on
	// every published event.
	serverImpl := getServerImpl(mcpServer)

	mcpServer.AddReceivingMiddleware(newTrackingMiddleware(mcpServer, projectID, opts, publishFn, serverImpl))

	registerGetMoreToolsIfEnabled(mcpServer, coreOpts)

	agentcat.LogSetupComplete(projectID, coreOpts)

	return newShutdownFn(), nil
}

// newShutdownFn returns an idempotent shutdown function that drains the
// publisher once, however many times (and from however many goroutines) it is
// called. Nothing else needs joining: capture is synchronous, so a call that
// has returned has already handed its event to the publisher, and there is no
// detached work left in flight.
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

// getMCPcat retrieves the AgentCatInstance associated with the given mcp.Server.
// Returns nil if the server has not been registered via Track.
func getMCPcat(mcpServer *mcp.Server) *agentcat.AgentCatInstance {
	return agentcat.GetInstance(mcpServer)
}

// unregisterServer removes the mcp.Server from the global tracking registry.
func unregisterServer(mcpServer *mcp.Server) {
	agentcat.UnregisterServer(mcpServer)
}

// Shutdown gracefully shuts down the global event publisher.
// The provided context controls the shutdown deadline.
func Shutdown(ctx context.Context) error {
	return agentcat.Shutdown(ctx)
}

// PublishCustomEvent publishes a customer-defined event to AgentCat.
// serverOrSessionID is either a tracked *mcp.Server (the event publishes
// without a session unless data.SessionID is set) or a session ID string used
// verbatim; projectID is required. See agentcat.PublishCustomEvent for
// details.
func PublishCustomEvent(serverOrSessionID any, projectID string, data *CustomEventData) error {
	return agentcat.PublishCustomEvent(serverOrSessionID, projectID, data)
}

// getServerImpl reads the server's unexported impl field reflectively. The
// official SDK exposes no accessor; AgentCat deliberately reaches into
// private state (CI change detection guards against upstream drift).
// Returns nil when the layout changes.
//
// The result is a COPY: the server holds the exact *mcp.Implementation the
// customer passed to mcp.NewServer, and we keep ours for the process lifetime
// and read it from every concurrent tool call — a customer mutating their own
// Implementation after Track() must not race with event capture. Event capture
// reads only the string fields (Name, Version), so a shallow copy suffices.
func getServerImpl(mcpServer *mcp.Server) (impl *mcp.Implementation) {
	defer func() {
		if r := recover(); r != nil {
			impl = nil
		}
	}()
	v := reflect.ValueOf(mcpServer).Elem().FieldByName("impl")
	if !v.IsValid() {
		return nil
	}
	v = reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	got, ok := v.Interface().(*mcp.Implementation)
	if !ok || got == nil {
		return nil
	}
	cp := *got
	return &cp
}
