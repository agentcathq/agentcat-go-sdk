package mcpgo

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
)

// addTracingToHooks registers AgentCat tracking hooks on the given Hooks struct.
// It captures request timing, session metadata, event creation, and publishing.
// The caller must call Stop() on the returned SessionMap during shutdown.
func addTracingToHooks(hooks *server.Hooks, opts *Options, publishFn func(*agentcat.Event)) *agentcat.SessionMap {
	requestTimes := &sync.Map{}
	sessionMap := agentcat.NewSessionMap(0)

	// getDuration calculates the duration since the request started and cleans up.
	getDuration := func(id any) *int32 {
		if startTime, ok := requestTimes.LoadAndDelete(id); ok {
			d := int32(time.Since(startTime.(time.Time)).Milliseconds())
			return &d
		}
		return nil
	}

	captureSession := func(ctx context.Context, request any, response any) *agentcat.ProtectedSession {
		return captureSessionFromContext(ctx, request, response, sessionMap, opts, publishFn)
	}

	// recoverHook guards every hook body: hooks run synchronously inside
	// mcp-go's request handling, which has no panic recovery of its own, so a
	// panic here would crash the customer's server. Recover, log, and drop
	// the event instead.
	recoverHook := func(hook string) {
		if r := recover(); r != nil {
			agentcat.LogRecoveredPanic("mcpgo "+hook+" hook", r)
		}
	}

	// AfterListTools: inject context params if enabled
	hooks.AddAfterListTools(func(ctx context.Context, id any, message *mcp.ListToolsRequest, result *mcp.ListToolsResult) {
		defer recoverHook("AfterListTools")

		shouldAddContext := false
		contextDescription := ""

		mcpServer := server.ServerFromContext(ctx)
		if mcpServer != nil {
			if tracker := agentcat.GetInstance(mcpServer); tracker != nil && tracker.Options != nil {
				shouldAddContext = !tracker.Options.DisableToolCallContext
				contextDescription = tracker.Options.CustomContextDescription
			}
		}

		if shouldAddContext {
			addContextParamsToToolsList(result, contextDescription)
		}
	})

	// When tracing is disabled, no events are captured or published; context
	// injection above still honors its own flag.
	if opts.DisableTracing {
		return sessionMap
	}

	// BeforeAny: store request start time
	hooks.AddBeforeAny(func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
		defer recoverHook("BeforeAny")
		requestTimes.Store(id, time.Now())
	})

	// OnSuccess: capture session, create and publish event
	hooks.AddOnSuccess(func(ctx context.Context, id any, method mcp.MCPMethod, message any, result any) {
		defer recoverHook("OnSuccess")

		duration := getDuration(id)

		ps := captureSession(ctx, message, result)
		if ps == nil {
			return
		}

		// Check if result is a CallToolResult with IsError=true
		isError := false
		var errorDetails error
		if toolResult, ok := result.(*mcp.CallToolResult); ok && toolResult.IsError {
			isError = true
			var errorMessages []string
			for _, content := range toolResult.Content {
				if textContent, ok := content.(mcp.TextContent); ok {
					errorMessages = append(errorMessages, textContent.Text)
				}
			}
			if len(errorMessages) > 0 {
				errorDetails = fmt.Errorf("%s", strings.Join(errorMessages, " "))
			}
		}

		// Map MCP method to event type
		eventType := fmt.Sprintf("mcp:%s", string(method))

		// Create event under lock (NewEvent reads session fields). The lock is
		// released via defer so a panic can never leave the session mutex held.
		evt := func() *agentcat.Event {
			ps.Mu.Lock()
			defer ps.Mu.Unlock()
			return agentcat.NewEvent(ps.Sess, eventType, duration, isError, errorDetails)
		}()

		if evt == nil {
			return
		}

		// Extract user intent from context parameter for tool calls
		if method == mcp.MethodToolsCall {
			userIntent := extractUserIntentFromRequest(message)
			if userIntent != "" {
				evt.UserIntent = &userIntent
			}
		}

		// Extract parameters and response data
		evt.Parameters = extractParameters(message)
		if result != nil && !isError {
			evt.Response = extractResponse(result)
		}

		// Extract transport-layer metadata (headers).
		if extra := extractExtra(message); extra != nil {
			if evt.Parameters == nil {
				evt.Parameters = make(map[string]any)
			}
			evt.Parameters["extra"] = extra
		}

		// Set resource name for resource-related methods
		if method == mcp.MethodResourcesRead {
			resourceName := extractResourceName(message)
			if resourceName != "" {
				evt.ResourceName = &resourceName
			}
		}

		// Set resource name for tool calls (tool name)
		if method == mcp.MethodToolsCall {
			toolName := extractToolName(message)
			if toolName != "" {
				evt.ResourceName = &toolName
			}
		}

		finishEvent(ctx, opts, message, ps, evt, publishFn)
	})

	// OnError: capture session, create and publish error event
	hooks.AddOnError(func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
		defer recoverHook("OnError")

		duration := getDuration(id)

		ps := captureSession(ctx, message, nil)
		if ps == nil {
			return
		}

		eventType := fmt.Sprintf("mcp:%s", string(method))

		// Create event under lock (NewEvent reads session fields). The lock is
		// released via defer so a panic can never leave the session mutex held.
		evt := func() *agentcat.Event {
			ps.Mu.Lock()
			defer ps.Mu.Unlock()
			return agentcat.NewEvent(ps.Sess, eventType, duration, true, err)
		}()

		if evt == nil {
			return
		}

		// Extract user intent from context parameter for tool calls
		if method == mcp.MethodToolsCall {
			userIntent := extractUserIntentFromRequest(message)
			if userIntent != "" {
				evt.UserIntent = &userIntent
			}
		}

		// Extract parameters even for error events
		evt.Parameters = extractParameters(message)

		// Extract transport-layer metadata (headers).
		if extra := extractExtra(message); extra != nil {
			if evt.Parameters == nil {
				evt.Parameters = make(map[string]any)
			}
			evt.Parameters["extra"] = extra
		}

		// Set resource name
		if method == mcp.MethodResourcesRead {
			resourceName := extractResourceName(message)
			if resourceName != "" {
				evt.ResourceName = &resourceName
			}
		}
		if method == mcp.MethodToolsCall {
			toolName := extractToolName(message)
			if toolName != "" {
				evt.ResourceName = &toolName
			}
		}

		finishEvent(ctx, opts, message, ps, evt, publishFn)
	})

	return sessionMap
}

// finishEvent applies the shared closing steps of the OnSuccess and OnError
// hooks: attach customer-defined tags and properties, backfill identity
// fields onto the event if Identify just ran (NewEvent may have been called
// before Identify populated the session), and publish.
func finishEvent(
	ctx context.Context,
	opts *Options,
	message any,
	ps *agentcat.ProtectedSession,
	evt *agentcat.Event,
	publishFn func(*agentcat.Event),
) {
	// Attach customer-defined tags and properties.
	attachEventMetadata(ctx, opts, message, evt)

	// Backfill identity under lock; the lock is released via defer so a panic
	// can never leave the session mutex held.
	func() {
		ps.Mu.Lock()
		defer ps.Mu.Unlock()
		if ps.Sess.IdentifyActorGivenId != nil && evt.IdentifyActorGivenId == nil {
			evt.IdentifyActorGivenId = ps.Sess.IdentifyActorGivenId
			evt.IdentifyActorName = ps.Sess.IdentifyActorName
			evt.IdentifyData = ps.Sess.IdentifyData
		}
	}()

	publishFn(evt)
}
