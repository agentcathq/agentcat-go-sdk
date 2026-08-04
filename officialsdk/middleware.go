package officialsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// newTrackingMiddleware intercepts tools/list (schema injection) and
// tools/call (handle resolution, stripping, mint-back, event capture).
// Every other method — server/discover included — passes through untouched
// and unpublished.
//
// Capture is SYNCHRONOUS, on the request's own goroutine, exactly as the mcpgo
// adapter does it. There is no capture-side queue and no drop arm: the one
// bounded queue in this SDK is the publisher's, which is the designed
// backpressure point and already drops with a warning. A second bound here
// meant an event could die in two places for two different reasons, and the
// adapter's own drop was pure data loss — the whole point of an analytics SDK
// is not to lose events.
//
// Safe because the go-sdk releases every call but `initialize` to run
// concurrently (jsonrpc2.Async, mcp/server.go:1911-1915) on its own goroutine
// (internal/jsonrpc2/conn.go:681-689), and responses to outgoing
// server→client calls are retired directly in the read loop
// (internal/jsonrpc2/conn.go:526-534) rather than queued behind handlers. So a
// customer callback may call back into the session without deadlocking, and a
// slow one delays only its own tool call.
func newTrackingMiddleware(
	serverRef *mcp.Server,
	projectID string,
	opts *Options,
	publishFn func(*agentcat.Event),
	serverImpl *mcp.Implementation,
) mcp.Middleware {
	agentcatVersion := agentcat.GetDependencyVersion(agentcat.SDKModulePath)

	return func(next mcp.MethodHandler) mcp.MethodHandler {
		stashRebuild(getMCPcat(serverRef), next)
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case "tools/list":
				result, err := next(ctx, method, req)
				func() {
					defer func() {
						if r := recover(); r != nil {
							agentcat.LogRecoveredPanic("officialsdk list injection", r)
						}
					}()
					if instance := getMCPcat(serverRef); instance != nil {
						handleToolsList(result, instance, agentcat.BuildInjectConfig(instance.Options, opts.ResolveSessionID != nil))
					}
				}()
				return result, err
			case "tools/call":
				return handleToolCall(ctx, req, next, serverRef, projectID, opts, publishFn, serverImpl, agentcatVersion)
			default:
				return next(ctx, method, req)
			}
		}
	}
}

// handleToolCall implements the v2 call path:
// resolve → strip (cloned) → dispatch → mint-back (wire only) → publish.
func handleToolCall(
	ctx context.Context,
	req mcp.Request,
	next mcp.MethodHandler,
	serverRef *mcp.Server,
	projectID string,
	opts *Options,
	publishFn func(*agentcat.Event),
	serverImpl *mcp.Implementation,
	agentcatVersion string,
) (mcp.Result, error) {
	ctr, ok := req.(*mcp.CallToolRequest)
	if !ok {
		return next(ctx, "tools/call", req)
	}

	instance := getMCPcat(serverRef)
	tracing := instance != nil && instance.Options != nil && !instance.Options.DisableTracing
	toolName := ctr.Params.Name

	// Raw arguments: recorded on the event exactly as the agent sent them.
	// Numbers decode as json.Number, not float64: the stripped dispatch below
	// re-marshals this map, and a float64 decode would silently corrupt
	// customer integers above 2^53 in both the dispatch and the event.
	rawArgs := map[string]any{}
	if len(ctr.Params.Arguments) > 0 {
		if m, ok := decodeJSONObject(ctr.Params.Arguments); ok {
			rawArgs = m
		}
	}

	// Registries: load, or rebuild on demand (fresh factory instance). This
	// MUST precede handle resolution: the registries are what say whether the
	// session_id argument is ours to read or the customer's own parameter.
	var reg *agentcat.Registries
	if instance != nil {
		reg = instance.Registries.Load()
		if reg == nil && instance.RebuildTools != nil {
			func() {
				defer func() {
					if r := recover(); r != nil {
						agentcat.LogRecoveredPanic("officialsdk registry rebuild", r)
					}
				}()
				if tools, err := instance.RebuildTools(ctx, ctr.Session); err == nil && tools != nil {
					_, rebuilt := agentcat.BuildInjectedTools(agentcat.BuildInjectConfig(instance.Options, opts.ResolveSessionID != nil), tools)
					reg = instance.MergeRegistries(rebuilt)
					agentcat.ReportSessionParamCollisions(instance, rebuilt)
				}
			}()
		}
	}

	// Per-call handle resolution (stateless). Guarded so a panic degrades to
	// an untracked dispatch, never a broken request.
	var (
		resolution agentcat.SessionResolution
		agentID    string
		mrtr       string
	)
	if tracing {
		func() {
			defer func() {
				if r := recover(); r != nil {
					agentcat.LogRecoveredPanic("officialsdk handle resolution", r)
					tracing = false
				}
			}()
			var hook func() (string, error)
			if opts.ResolveSessionID != nil {
				hook = func() (string, error) { return opts.ResolveSessionID(ctx, req) }
			}
			resolution = agentcat.ResolveSessionHandle(rawArgs, hook, projectID,
				agentcat.SessionParamIsOurs(toolName, reg))
			if opts.EnableAgentTracking {
				agentID, _ = agentcat.ExtractHandle(rawArgs, agentcat.ParamAgentID)
			}
			if hasInputResponses(ctr.Params) {
				mrtr = agentcat.MRTRContinuation
			}
		}()
	}

	// Strip injected params on a CLONE; the customer's request is untouched.
	stripped := stripArgumentsSafely(rawArgs, func() map[string]any {
		return agentcat.StripToolArguments(toolName, rawArgs, reg)
	})
	dispatchReq := ctr
	if len(rawArgs) != len(stripped) {
		if cleaned, err := json.Marshal(stripped); err == nil {
			paramsCopy := *ctr.Params
			paramsCopy.Arguments = cleaned
			reqCopy := *ctr
			reqCopy.Params = &paramsCopy
			dispatchReq = &reqCopy
		}
	}

	start := time.Now()
	result, err := next(ctx, "tools/call", dispatchReq)
	ms := time.Since(start).Milliseconds()
	if ms > math.MaxInt32 {
		ms = math.MaxInt32
	}
	duration := int32(ms)

	toolResult, _ := result.(*mcp.CallToolResult)

	// MRTR intermediate round: tag, and never decorate — the completing
	// round carries the mint-back.
	inputRequired := resultNeedsInput(toolResult)
	if inputRequired {
		mrtr = agentcat.MRTRInputRequired
	}

	// Wire-only mint-back on a copied result.
	wireResult := result
	if tracing && toolResult != nil && !inputRequired {
		func() {
			defer func() {
				if r := recover(); r != nil {
					agentcat.LogRecoveredPanic("officialsdk mint-back", r)
				}
			}()
			wireResult = decorateResult(toolResult, resolution, agentID, toolName, reg)
		}()
	}

	// Capture synchronously, on this request's own goroutine. The event records
	// the raw request and the original (undecorated) result; Publish itself is
	// non-blocking, so the only queue in the path is the publisher's — the one
	// designed to absorb backpressure and to say so when it cannot.
	if tracing {
		userIntent, _ := rawArgs[agentcat.ParamContext].(string)
		if instance.Options.DisableToolCallContext {
			userIntent = ""
		}
		func() {
			// Capture now runs inside the customer's request, so a panic here
			// would surface as a failed tool call. Recover, log, drop the
			// event — the call still returns normally.
			defer func() {
				if r := recover(); r != nil {
					agentcat.LogRecoveredPanic("officialsdk event capture", r)
				}
			}()
			captureToolCallEvent(ctx, ctr, result, err, duration, projectID, opts,
				publishFn, serverImpl, agentcatVersion, resolution, agentID, mrtr, userIntent, rawArgs)
		}()
	}

	return wireResult, err
}

// stripArgumentsSafely runs the argument strip under a panic guard, like every
// other seam on the synchronous request path. On panic it returns the raw
// arguments so the call dispatches unmodified — never broken.
func stripArgumentsSafely(rawArgs map[string]any, strip func() map[string]any) (stripped map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			agentcat.LogRecoveredPanic("officialsdk argument stripping", r)
			stripped = rawArgs
		}
	}()
	return strip()
}

// decorateResult applies the text mint-back and structured mirror to a COPY
// of the customer's result. The original result object is what the event
// records; the copy is what goes on the wire.
func decorateResult(
	res *mcp.CallToolResult,
	resolution agentcat.SessionResolution,
	agentID string,
	toolName string,
	reg *agentcat.Registries,
) mcp.Result {
	cp := *res
	cp.Content = append([]mcp.Content(nil), res.Content...)

	// (a) Trailing text block: on the call that minted a new session, and on
	// one that sent a session_id this server never issued. BuildMintBackText
	// owns that decision — hook mode, foreign and supplied all return "".
	// Applied unconditionally otherwise: to error results (the retry after an
	// error must carry the same session) and to tools that return no content
	// at all (structured-only tools would otherwise never learn their session,
	// and every call would mint a fresh one).
	if text := agentcat.BuildMintBackText(resolution); text != "" {
		cp.Content = append(cp.Content, &mcp.TextContent{Text: text})
	}

	// (b) Structured mirror: persistent handle state on every response,
	// gated on the output-injection registry, plain-object structuredContent
	// only, customer's own _mcp_instructions key wins.
	mirror := agentcat.BuildHandleMirror(agentcat.MirrorInput{
		Resolution: resolution,
		AgentID:    agentID,
	})
	if mirror != nil && agentcat.ShouldMirror(toolName, reg) {
		if sc, ok := structuredContentAsMap(res.StructuredContent); ok {
			if _, customerOwns := sc[agentcat.MCPInstructionsKey]; !customerOwns {
				sc[agentcat.MCPInstructionsKey] = mirror
				cp.StructuredContent = sc
			}
		}
	}
	return &cp
}

// structuredContentAsMap returns a COPY of the result's structuredContent as
// a plain JSON object, or ok=false when it is absent or not an object. The
// typed AddTool path stores json.RawMessage here (the go-sdk marshals the
// handler's output once); manual handlers usually store map[string]any.
func structuredContentAsMap(sc any) (map[string]any, bool) {
	switch v := sc.(type) {
	case nil:
		return nil, false
	case map[string]any:
		cp := make(map[string]any, len(v)+1)
		for k, val := range v {
			cp[k] = val
		}
		return cp, true
	default:
		// json.RawMessage or an arbitrary marshalable value: the round trip
		// doubles as the copy. Non-objects (arrays, primitives, null) stay
		// untouched.
		raw, err := json.Marshal(sc)
		if err != nil {
			return nil, false
		}
		return decodeJSONObject(raw)
	}
}

// decodeJSONObject decodes raw JSON into a map. Numbers decode as json.Number
// so they re-marshal as the customer's original literal — decoding them as
// float64 would change large integers (and break an "type":"integer" output
// schema). Non-objects (arrays, primitives, null) are rejected untouched.
func decodeJSONObject(raw []byte) (map[string]any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// captureToolCallEvent builds and publishes the single mcp:tools/call event.
func captureToolCallEvent(
	ctx context.Context,
	ctr *mcp.CallToolRequest,
	result mcp.Result,
	callErr error,
	duration int32,
	projectID string,
	opts *Options,
	publishFn func(*agentcat.Event),
	serverImpl *mcp.Implementation,
	agentcatVersion string,
	resolution agentcat.SessionResolution,
	agentID string,
	mrtr string,
	userIntent string,
	rawArgs map[string]any,
) {
	isError := callErr != nil
	errorDetails := callErr
	if !isError {
		if toolResult, ok := result.(*mcp.CallToolResult); ok && toolResult.IsError {
			isError = true
			var msgs []string
			for _, c := range toolResult.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					msgs = append(msgs, tc.Text)
				}
			}
			if len(msgs) > 0 {
				errorDetails = fmt.Errorf("%s", strings.Join(msgs, " "))
			}
		}
	}

	ec := &agentcat.EventContext{
		ProjectID:       projectID,
		SessionID:       resolution.SessionID,
		SessionSource:   string(resolution.Source),
		ProtocolVersion: protocolVersionOf(ctr),
		AgentID:         agentID,
		MRTR:            mrtr,
		SDKLanguage:     "Go",
		AgentcatVersion: agentcatVersion,
	}
	// Per-request client identity: _meta reserved keys first, then the
	// initialize capture, so one path covers 2026 and pre-2026 clients.
	// go-sdk v1.7.0 runs that ladder itself; compat.go rebuilds it on older
	// versions. Narrow defensively.
	if info := clientInfoOf(ctr); info != nil {
		ec.ClientName = info.Name
		ec.ClientVersion = info.Version
	}
	if serverImpl != nil {
		ec.ServerName = serverImpl.Name
		ec.ServerVersion = serverImpl.Version
	}

	// Identify runs on EVERY tool call; result stamps this event only. An
	// identity without a user ID names no actor, so it is dropped rather than
	// stamping an empty actor onto the event.
	if opts.Identify != nil {
		if identity := safeIdentify(ctx, opts, ctr); identity != nil && identity.UserID != "" {
			ec.Identity = identity
		}
	}

	evt := agentcat.NewToolCallEvent(ec, &duration, isError, errorDetails)
	if evt == nil {
		return
	}
	if userIntent != "" {
		evt.UserIntent = &userIntent
	}
	if toolName := ctr.Params.Name; toolName != "" {
		evt.ResourceName = &toolName
	}

	// Raw, unstripped request — exactly what the agent sent.
	evt.Parameters = map[string]any{"name": ctr.Params.Name, "arguments": rawArgs}
	if extra := extractExtra(ctr); extra != nil {
		evt.Parameters["extra"] = extra
	}
	if result != nil && !isError {
		evt.Response = extractResponse("tools/call", result)
	}

	// Customer tags/properties first, then SDK tags (SDK wins, cap-exempt).
	attachEventMetadata(ctx, opts, ctr, evt)
	agentcat.ApplySDKTags(evt, ec)

	publishFn(evt)
}

// safeIdentify invokes the user-supplied Identify callback with panic
// recovery so a faulty callback can never break event capture.
func safeIdentify(ctx context.Context, opts *Options, req mcp.Request) (identity *agentcat.UserIdentity) {
	defer func() {
		if r := recover(); r != nil {
			agentcat.LogRecoveredPanic("officialsdk Identify callback", r)
			identity = nil
		}
	}()
	return opts.Identify(ctx, req)
}
