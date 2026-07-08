// Package officialsdk provides AgentCat tracking integration for the official
// Go MCP SDK (github.com/modelcontextprotocol/go-sdk).
//
// It installs receiving middleware on an mcp.Server that automatically captures
// tool calls, resource reads, prompt requests, and other MCP protocol events
// and publishes them to AgentCat.
package officialsdk

import (
	"context"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk"
)

// Re-export core types so users don't need to import the core module directly.
type (
	UserIdentity     = agentcat.UserIdentity
	AgentCatInstance = agentcat.AgentCatInstance
	CustomEventData  = agentcat.CustomEventData
	ExporterConfig   = agentcat.ExporterConfig
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
	// AgentCat. Context parameter injection and the get_more_tools tool still
	// honor their own flags.
	DisableTracing bool

	// CustomContextDescription overrides the default description of the
	// injected "context" parameter. Use it to provide domain-specific guidance
	// to LLMs about what context they should provide. Only applies when tool
	// call context injection is enabled.
	CustomContextDescription string

	// Debug enables debug logging to ~/agentcat.log. When false, no logging occurs.
	Debug bool

	// Identify is called on every tool call to identify the actor. The result
	// is compared against the session's current identity; an identify event is
	// published only when the identity changes (UserID/UserName are
	// overwritten, UserData is merged). Return nil to skip identification.
	Identify func(ctx context.Context, request *mcp.CallToolRequest) *UserIdentity

	// EventTags is called on every auto-captured event to attach string
	// key-value tags. It receives the MCP request that triggered the event.
	// Tags are validated client-side: keys must be at most 32 chars matching
	// [a-zA-Z0-9$_.:\- ], values at most 200 chars with no newlines, at most
	// 50 entries per event. Invalid entries are dropped with a warning logged.
	// If the callback panics or returns nil/empty, tags are omitted.
	EventTags func(ctx context.Context, request mcp.Request) map[string]string

	// EventProperties is called on every auto-captured event to attach
	// arbitrary JSON metadata (no validation is applied). It receives the MCP
	// request that triggered the event. If the callback panics or returns
	// nil/empty, properties are omitted.
	EventProperties func(ctx context.Context, request mcp.Request) map[string]any

	// RedactSensitiveInformation redacts sensitive data before sending to AgentCat.
	RedactSensitiveInformation func(text string) string

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

	apiBaseURL := agentcat.ResolveAPIBaseURL(opts.APIBaseURL)

	coreOpts := &agentcat.Options{
		DisableReportMissing:       opts.DisableReportMissing,
		DisableToolCallContext:     opts.DisableToolCallContext,
		DisableTracing:             opts.DisableTracing,
		CustomContextDescription:   opts.CustomContextDescription,
		Debug:                      opts.Debug,
		RedactSensitiveInformation: opts.RedactSensitiveInformation,
		DisableDiagnostics:         opts.DisableDiagnostics,
		APIBaseURL:                 apiBaseURL,
		Exporters:                  opts.Exporters,
	}

	instance := &agentcat.AgentCatInstance{
		ProjectID: projectID,
		Options:   coreOpts,
		ServerRef: mcpServer,
		SessionID: agentcat.NewSessionID(),
	}
	agentcat.RegisterServer(mcpServer, instance)
	agentcat.SetDebug(opts.Debug)

	publishFn := agentcat.InitPublisher(opts.RedactSensitiveInformation, apiBaseURL, opts.Exporters)

	// Retrieve the server implementation for session metadata.
	// We store a copy of the implementation info at Track() time.
	serverImpl := getServerImpl(mcpServer)

	middleware, sessionMap := newTrackingMiddleware(projectID, opts, publishFn, serverImpl)
	mcpServer.AddReceivingMiddleware(middleware)

	registerGetMoreToolsIfEnabled(mcpServer, coreOpts)

	var once sync.Once
	shutdownFn := func(ctx context.Context) error {
		var err error
		once.Do(func() {
			sessionMap.Stop()
			err = agentcat.Shutdown(ctx)
		})
		return err
	}

	agentcat.LogSetupComplete(projectID, coreOpts)

	return shutdownFn, nil
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
// serverOrSessionID is either a tracked *mcp.Server or an MCP session ID
// string; projectID is required. See agentcat.PublishCustomEvent for details.
func PublishCustomEvent(serverOrSessionID any, projectID string, data *CustomEventData) error {
	return agentcat.PublishCustomEvent(serverOrSessionID, projectID, data)
}

// getServerImpl extracts the Implementation from the server.
// The official SDK does not directly expose the impl field, so we rely on the
// fact that the server was created with NewServer(impl, opts).
// We store the values passed by the caller at Track() time to avoid needing
// reflection.  For now, we return nil and let session.go handle the nil case
// gracefully.  When the SDK exposes an accessor, this can be updated.
func getServerImpl(mcpServer *mcp.Server) *mcp.Implementation {
	// The official SDK does not expose a public accessor for the Implementation.
	// We return nil here; session metadata will be populated from the
	// InitializeParams of the ServerSession instead.
	_ = mcpServer
	return nil
}
