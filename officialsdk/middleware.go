package officialsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk"
)

const maxCaptureConcurrency = 100

// newTrackingMiddleware creates a single mcp.Middleware that intercepts all
// incoming requests and captures AgentCat events.
// The caller must call Stop() on the returned SessionMap during shutdown.
func newTrackingMiddleware(
	projectID string,
	opts *Options,
	publishFn func(*agentcat.Event),
	serverImpl *mcp.Implementation,
) (mcp.Middleware, *agentcat.SessionMap) {
	sessionMap := agentcat.NewSessionMap(0)
	captureSem := make(chan struct{}, maxCaptureConcurrency)

	mw := func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			startTime := time.Now()

			var userIntent string
			if method == "tools/call" && !opts.DisableToolCallContext {
				userIntent = safeStripContextParam(req)
			}

			result, err := next(ctx, method, req)

			ms := time.Since(startTime).Milliseconds()
			if ms > math.MaxInt32 {
				ms = math.MaxInt32
			}
			duration := int32(ms)

			if method == "tools/list" && !opts.DisableToolCallContext {
				safeInjectContextParams(result, opts.CustomContextDescription)
			}

			// When tracing is disabled, no events are captured or published;
			// context injection above still honors its own flag.
			if opts.DisableTracing {
				return result, err
			}

			// Capture event asynchronously with bounded concurrency.
			// If the semaphore is full, skip capture to avoid goroutine buildup.
			detachedCtx := context.WithoutCancel(ctx)
			select {
			case captureSem <- struct{}{}:
				go func() {
					defer func() { <-captureSem }()
					// A panic in an unattended goroutine kills the whole
					// process; recover, log, and drop the event instead.
					defer func() {
						if r := recover(); r != nil {
							agentcat.LogRecoveredPanic("officialsdk capture goroutine", r)
						}
					}()
					captureEvent(detachedCtx, method, req, result, err, &duration, projectID, opts, publishFn, sessionMap, serverImpl, userIntent)
				}()
			default:
				// Too many concurrent capture goroutines; drop this event.
			}

			return result, err
		}
	}

	return mw, sessionMap
}

// safeStripContextParam runs stripContextParam with panic recovery: it runs
// synchronously on the customer's request path, so a panic here must degrade
// to "no user intent captured", never crash the request.
func safeStripContextParam(req mcp.Request) (userIntent string) {
	defer func() {
		if r := recover(); r != nil {
			agentcat.LogRecoveredPanic("officialsdk stripContextParam", r)
			userIntent = ""
		}
	}()
	return stripContextParam(req)
}

// safeInjectContextParams runs injectContextParams with panic recovery: it
// runs synchronously on the customer's request path, so a panic here must
// degrade to "no context param injected", never crash the request.
func safeInjectContextParams(result mcp.Result, customDescription string) {
	defer func() {
		if r := recover(); r != nil {
			agentcat.LogRecoveredPanic("officialsdk injectContextParams", r)
		}
	}()
	injectContextParams(result, customDescription)
}

// stripContextParam extracts the "context" value from a tools/call request's
// arguments and removes it from the request in-place. This prevents the go-sdk
// schema validation from rejecting the injected parameter. Returns the
// extracted context value (empty string if not present).
//
// Tools that natively define "context" (like get_more_tools) are skipped.
func stripContextParam(req mcp.Request) string {
	toolReq, ok := req.(*mcp.CallToolRequest)
	if !ok || toolReq.Params == nil || len(toolReq.Params.Arguments) == 0 {
		return ""
	}

	// Don't strip context from tools that define it natively.
	if toolReq.Params.Name == "get_more_tools" {
		return ""
	}

	var args map[string]any
	if err := json.Unmarshal(toolReq.Params.Arguments, &args); err != nil {
		return ""
	}

	contextVal, _ := args["context"].(string)
	if contextVal == "" {
		return ""
	}

	delete(args, "context")
	cleaned, err := json.Marshal(args)
	if err != nil {
		return contextVal
	}
	toolReq.Params.Arguments = cleaned
	return contextVal
}

// captureEvent creates and publishes an AgentCat event from the middleware context.
func captureEvent(
	ctx context.Context,
	method string,
	req mcp.Request,
	result mcp.Result,
	callErr error,
	duration *int32,
	projectID string,
	opts *Options,
	publishFn func(*agentcat.Event),
	sessionMap *agentcat.SessionMap,
	serverImpl *mcp.Implementation,
	userIntent string,
) {
	// Build session
	ps := getOrCreateSession(req, sessionMap, serverImpl, projectID)
	if ps == nil {
		return
	}

	// Lock the session for all field reads/writes; the lock is released via
	// defer (before the user-callback section below) so a panic can never
	// leave the session mutex held.
	var evt *agentcat.Event
	var toolReq *mcp.CallToolRequest
	func() {
		ps.Mu.Lock()
		defer ps.Mu.Unlock()

		// For initialize responses, capture server info from the result
		if method == "initialize" && result != nil {
			updateSessionFromInitResult(ps, result)
		}

		// Determine error state
		isError := callErr != nil
		var errorDetails error

		if callErr != nil {
			errorDetails = callErr
		}

		// Check CallToolResult.IsError for tool-level errors
		if !isError && result != nil {
			if toolResult, ok := result.(*mcp.CallToolResult); ok && toolResult.IsError {
				isError = true
				var errorMessages []string
				for _, content := range toolResult.Content {
					if textContent, ok := content.(*mcp.TextContent); ok {
						errorMessages = append(errorMessages, textContent.Text)
					}
				}
				if len(errorMessages) > 0 {
					errorDetails = fmt.Errorf("%s", strings.Join(errorMessages, " "))
				}
			}
		}

		// Map MCP method to event type
		eventType := fmt.Sprintf("mcp:%s", method)

		// Create event using core API
		evt = agentcat.NewEvent(ps.Sess, eventType, duration, isError, errorDetails)
		if evt == nil {
			return
		}

		// Set user intent from context parameter (extracted before schema validation).
		if method == "tools/call" && userIntent != "" {
			evt.UserIntent = &userIntent
		}

		// Extract parameters and response data
		evt.Parameters = extractParameters(method, req)
		if result != nil && !isError {
			evt.Response = extractResponse(method, result)
		}

		// Extract transport-layer metadata (headers, token info).
		if extra := extractExtra(req); extra != nil {
			if evt.Parameters == nil {
				evt.Parameters = make(map[string]any)
			}
			evt.Parameters["extra"] = extra
		}

		// Set resource name for resource-related methods
		if method == "resources/read" {
			resourceURI := extractResourceURI(req)
			if resourceURI != "" {
				evt.ResourceName = &resourceURI
			}
		}

		// Set resource name for tool calls (tool name)
		if method == "tools/call" {
			toolName := extractToolName(req)
			if toolName != "" {
				evt.ResourceName = &toolName
			}
		}

		// Determine whether identify should run for this request while holding
		// the lock, but release the lock before calling the user's Identify
		// callback (which may be slow or block).
		if method == "tools/call" && opts != nil && opts.Identify != nil {
			toolReq, _ = req.(*mcp.CallToolRequest)
		}
	}()

	if evt == nil {
		return
	}

	// Identify runs on every tool call; an identify event is published only
	// when the identity changes (UserID/UserName overwritten, UserData merged).
	if toolReq != nil {
		handleIdentify(ctx, opts, toolReq, ps, evt, publishFn)
	}

	// Attach customer-defined tags and properties.
	attachEventMetadata(ctx, opts, req, evt)

	publishFn(evt)
}

// handleIdentify runs the Identify callback for a tool call, compares the
// result against the session's current identity, and publishes an identify
// event only when the identity changed. The merged identity is also copied
// onto the in-flight event. A panic in the callback is swallowed.
func handleIdentify(
	ctx context.Context,
	opts *Options,
	toolReq *mcp.CallToolRequest,
	ps *agentcat.ProtectedSession,
	evt *agentcat.Event,
	publishFn func(*agentcat.Event),
) {
	identifyInfo := safeIdentify(ctx, opts, toolReq)
	if identifyInfo == nil {
		return
	}

	// Merge and compare under lock; the lock is released via defer so a panic
	// (e.g. a user MarshalJSON inside IdentitiesEqual) can never leave the
	// session mutex held.
	var merged *agentcat.UserIdentity
	var identifyEvent *agentcat.Event
	func() {
		ps.Mu.Lock()
		defer ps.Mu.Unlock()

		merged = agentcat.MergeIdentities(ps.Identity, identifyInfo)
		changed := ps.Identity == nil || !agentcat.IdentitiesEqual(ps.Identity, merged)
		ps.Identity = merged

		ps.Sess.IdentifyActorGivenId = &merged.UserID
		ps.Sess.IdentifyActorName = &merged.UserName
		ps.Sess.IdentifyData = merged.UserData

		if changed {
			identifyEvent = agentcat.CreateIdentifyEvent(ps.Sess)
		}
	}()

	// Copy the merged identity onto the in-flight event.
	evt.IdentifyActorGivenId = &merged.UserID
	evt.IdentifyActorName = &merged.UserName
	evt.IdentifyData = merged.UserData

	if identifyEvent != nil {
		publishFn(identifyEvent)
	}
}

// safeIdentify invokes the user-supplied Identify callback with panic
// recovery so a faulty callback can never break the customer's server.
func safeIdentify(ctx context.Context, opts *Options, toolReq *mcp.CallToolRequest) (identity *agentcat.UserIdentity) {
	defer func() {
		if r := recover(); r != nil {
			identity = nil
		}
	}()
	return opts.Identify(ctx, toolReq)
}
