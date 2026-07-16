// Package mcpgo provides MCPCat tracking integration for mark3labs/mcp-go servers.
//
// It wraps an MCPServer with hooks that automatically capture tool calls,
// resource reads, and other MCP protocol events and publishes them to MCPCat.
package mcpgo

import (
	"context"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
)

// Re-export core types so users don't need to import the core module directly.
type (
	UserIdentity   = agentcat.UserIdentity
	MCPcatInstance = agentcat.MCPcatInstance
)

// Options configures MCPCat tracking for mark3labs/mcp-go servers.
type Options struct {
	// Hooks provides pre-existing server hooks to append MCPCat's hooks to.
	// If nil, a new Hooks struct is created.
	Hooks *server.Hooks

	// DisableReportMissing, when true, prevents the automatic "get_more_tools"
	// tool from being registered. By default (false) the tool is added.
	DisableReportMissing bool

	// DisableToolCallContext, when true, prevents the "context" parameter from
	// being injected into existing tools. By default (false) it is added.
	DisableToolCallContext bool

	// Debug enables debug logging to ~/mcpcat.log. When false, no logging occurs.
	Debug bool

	// Identify is called once per session to identify the actor.
	// It receives the context and the CallToolRequest that triggered the identification.
	// Return nil to skip identification for this session.
	Identify func(ctx context.Context, request *mcp.CallToolRequest) *UserIdentity

	// RedactSensitiveInformation redacts sensitive data before sending to MCPCat.
	RedactSensitiveInformation func(text string) string

	// RedactEvent is the event-level redaction hook, invoked with the full
	// event (inspect ResourceName, EventType, Parameters, Response, etc.)
	// before it is published. Return a modified event, or nil to drop the
	// event entirely. Runs before RedactSensitiveInformation, so it sees
	// raw, unredacted values. The system-managed fields Id, SessionId,
	// ProjectId, EventType, and Timestamp cannot be changed. If the hook
	// returns an error or panics, the event is dropped and the error is
	// logged.
	RedactEvent func(event *agentcat.Event) (*agentcat.Event, error)

	// DisableDiagnostics disables MCPCat's internal SDK diagnostics. On by default;
	// also disable via the DISABLE_DIAGNOSTICS env var. ~/mcpcat.log is unaffected.
	DisableDiagnostics bool

	// APIBaseURL overrides the default MCPCat API endpoint.
	// When empty, the SDK falls back to the MCPCAT_API_URL environment variable,
	// and then to the built-in default (https://api.mcpcat.io).
	APIBaseURL string
}

// DefaultOptions returns a new Options with sensible defaults.
// All features are enabled by default (Disable* fields are false).
func DefaultOptions() *Options {
	return &Options{}
}

// Track attaches MCPCat tracking hooks to the given MCPServer.
// It registers the server in the global registry, initializes the event
// publisher, and wires up hooks for request timing, event capture, context
// parameter injection, and the optional get_more_tools tool.
//
// On success it returns a shutdown function that flushes pending events and
// releases resources. The shutdown function is idempotent and safe to call
// multiple times. On error it returns (nil, err).
func Track(mcpServer *server.MCPServer, projectID string, opts *Options) (func(context.Context) error, error) {
	if opts == nil {
		opts = DefaultOptions()
	}

	agentcat.InitDiagnostics(projectID, opts.DisableDiagnostics, "mcpgo",
		"github.com/mark3labs/mcp-go")

	if mcpServer == nil {
		agentcat.LogSetupFailed("server must not be nil")
		return nil, agentcat.ErrNilServer
	}
	if projectID == "" {
		agentcat.LogSetupFailed("projectID must not be empty")
		return nil, agentcat.ErrEmptyProjectID
	}

	hooks := &server.Hooks{}
	if opts.Hooks != nil {
		hooks = opts.Hooks
	}
	server.WithHooks(hooks)(mcpServer)

	apiBaseURL := agentcat.ResolveAPIBaseURL(opts.APIBaseURL)

	coreOpts := &agentcat.Options{
		DisableReportMissing:       opts.DisableReportMissing,
		DisableToolCallContext:     opts.DisableToolCallContext,
		Debug:                      opts.Debug,
		RedactSensitiveInformation: opts.RedactSensitiveInformation,
		RedactEvent:                opts.RedactEvent,
		DisableDiagnostics:         opts.DisableDiagnostics,
		APIBaseURL:                 apiBaseURL,
	}

	instance := &agentcat.MCPcatInstance{
		ProjectID: projectID,
		Options:   coreOpts,
		ServerRef: mcpServer,
	}
	agentcat.RegisterServer(mcpServer, instance)
	agentcat.SetDebug(opts.Debug)

	publishFn := agentcat.InitPublisher(opts.RedactSensitiveInformation, opts.RedactEvent, apiBaseURL)

	sessionMap := addTracingToHooks(hooks, opts, publishFn)
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

// getMCPcat retrieves the MCPcatInstance associated with the given MCPServer.
// Returns nil if the server has not been registered via Track.
func getMCPcat(mcpServer *server.MCPServer) *agentcat.MCPcatInstance {
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
