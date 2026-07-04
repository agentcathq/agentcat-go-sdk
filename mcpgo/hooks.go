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

// addTracingToHooks registers MCPCat tracking hooks on the given Hooks struct.
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

	// BeforeAny: store request start time
	hooks.AddBeforeAny(func(ctx context.Context, id any, method mcp.MCPMethod, message any) {
		requestTimes.Store(id, time.Now())
	})

	// AfterListTools: inject context params if enabled
	hooks.AddAfterListTools(func(ctx context.Context, id any, message *mcp.ListToolsRequest, result *mcp.ListToolsResult) {
		shouldAddContext := false

		mcpServer := server.ServerFromContext(ctx)
		if mcpServer != nil {
			if tracker := agentcat.GetInstance(mcpServer); tracker != nil && tracker.Options != nil {
				shouldAddContext = !tracker.Options.DisableToolCallContext
			}
		}

		if shouldAddContext {
			addContextParamsToToolsList(result)
		}
	})

	// OnSuccess: capture session, create and publish event
	hooks.AddOnSuccess(func(ctx context.Context, id any, method mcp.MCPMethod, message any, result any) {
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

		// Create event under lock (NewEvent reads session fields)
		ps.Mu.Lock()
		evt := agentcat.NewEvent(ps.Sess, eventType, duration, isError, errorDetails)
		ps.Mu.Unlock()

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

		// Ensure identity fields are on the event if Identify just ran.
		// NewEvent may have been called before Identify populated the session.
		ps.Mu.Lock()
		if ps.Sess.IdentifyActorGivenId != nil && evt.IdentifyActorGivenId == nil {
			evt.IdentifyActorGivenId = ps.Sess.IdentifyActorGivenId
			evt.IdentifyActorName = ps.Sess.IdentifyActorName
			evt.IdentifyData = ps.Sess.IdentifyData
		}
		ps.Mu.Unlock()

		publishFn(evt)
	})

	// OnError: capture session, create and publish error event
	hooks.AddOnError(func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
		duration := getDuration(id)

		ps := captureSession(ctx, message, nil)
		if ps == nil {
			return
		}

		eventType := fmt.Sprintf("mcp:%s", string(method))

		// Create event under lock (NewEvent reads session fields)
		ps.Mu.Lock()
		evt := agentcat.NewEvent(ps.Sess, eventType, duration, true, err)
		ps.Mu.Unlock()

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

		// Ensure identity fields are on the event if Identify just ran.
		ps.Mu.Lock()
		if ps.Sess.IdentifyActorGivenId != nil && evt.IdentifyActorGivenId == nil {
			evt.IdentifyActorGivenId = ps.Sess.IdentifyActorGivenId
			evt.IdentifyActorName = ps.Sess.IdentifyActorName
			evt.IdentifyData = ps.Sess.IdentifyData
		}
		ps.Mu.Unlock()

		publishFn(evt)
	})

	return sessionMap
}
