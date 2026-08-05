package mcpgo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
	"unsafe"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/v2"
)

// capturer holds everything the two capture sites need: the tool middleware
// (which sees every call that reaches a handler) and the failure hooks (which
// see the calls mcp-go rejects before the handler, and so before the
// middleware).
//
// Ownership contract: the server's own tool middleware is the capturer's ONLY
// strong owner; the hook_state side map reaches it weakly. That is why the
// strong server field below is safe — the capturer lives inside a
// server-owned cycle, never pinned from a package-level root — and it is what
// keeps a dead server's capturer (and anything a customer callback captured)
// collectible even though the customer's shared Hooks value lives on.
type capturer struct {
	server          *server.MCPServer
	projectID       string
	opts            *Options
	publish         func(*agentcat.Event)
	serverName      string
	serverVersion   string
	agentcatVersion string

	// pending carries the per-call state the middleware resolved to the
	// after-hook that publishes the event.
	pending pendingCalls
}

// userIntent extracts the captured intent from the raw arguments, honouring
// the customer's context-injection setting.
func (c *capturer) userIntent(rawArgs map[string]any) string {
	intent, _ := rawArgs[agentcat.ParamContext].(string)
	if instance := agentcat.GetInstance(c.server); instance != nil && instance.Options != nil &&
		instance.Options.DisableToolCallContext {
		return ""
	}
	return intent
}

// newCapturer resolves the once-per-server capture context at Track time.
func newCapturer(mcpServer *server.MCPServer, projectID string, opts *Options, publishFn func(*agentcat.Event)) *capturer {
	serverName, serverVersion := getServerInfo(mcpServer)
	return &capturer{
		server:          mcpServer,
		projectID:       projectID,
		opts:            opts,
		publish:         publishFn,
		serverName:      serverName,
		serverVersion:   serverVersion,
		agentcatVersion: agentcat.GetDependencyVersion(agentcat.SDKModulePath),
	}
}

// tracingEnabled reports whether events may be published for this server right
// now (the instance is still registered and tracing is not disabled).
func (c *capturer) tracingEnabled() bool {
	instance := agentcat.GetInstance(c.server)
	return instance != nil && instance.Options != nil && !instance.Options.DisableTracing
}

// resolveHandles performs the stateless per-call handle resolution shared by
// both capture sites. ok is false when a panic forced the call to go untracked.
//
// reg decides whether the session_id argument is AgentCat's to read; callers
// must load the registries first. A nil reg means "ours", which validates.
func (c *capturer) resolveHandles(ctx context.Context, request mcp.CallToolRequest, reg *agentcat.Registries) (resolution agentcat.SessionResolution, agentID string, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			agentcat.LogRecoveredPanic("mcpgo handle resolution", r)
			ok = false
		}
	}()

	var hook func() (string, error)
	if c.opts.ResolveSessionID != nil {
		hook = func() (string, error) { return c.opts.ResolveSessionID(ctx, request) }
	}
	rawArgs := request.GetArguments()
	resolution = agentcat.ResolveSessionHandle(rawArgs, hook, c.projectID,
		agentcat.SessionParamIsOurs(request.Params.Name, reg))
	if c.opts.EnableAgentTracking {
		agentID, _ = agentcat.ExtractHandle(rawArgs, agentcat.ParamAgentID)
	}
	return resolution, agentID, true
}

// loadRegistries returns the registries already stored for this server, or
// nil. Unlike the middleware's path it never triggers a rebuild: the failure
// hooks call this, and driving a synthetic tools/list out of an error handler
// is a side effect nothing on that path consented to. Nil means the session
// parameter counts as ours, which validates rather than disowns.
func (c *capturer) loadRegistries() *agentcat.Registries {
	if instance := agentcat.GetInstance(c.server); instance != nil {
		return instance.Registries.Load()
	}
	return nil
}

// newToolMiddleware implements the v2 call path as an mcp-go tool handler
// middleware: resolve → strip (cloned) → dispatch → mint-back (wire only)
// → publish. Installed via MCPServer.Use at Track time. Every other MCP
// method passes through untouched and unpublished.
func newToolMiddleware(c *capturer) server.ToolHandlerMiddleware {
	mcpServer := c.server

	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			instance := agentcat.GetInstance(mcpServer)
			tracing := instance != nil && instance.Options != nil && !instance.Options.DisableTracing
			toolName := request.Params.Name
			// Raw arguments: recorded on the event exactly as the agent sent them.
			rawArgs := request.GetArguments()

			// Registries: load, or rebuild on demand (a call landing on an
			// instance that never served tools/list). This MUST precede handle
			// resolution: the registries are what say whether the session_id
			// argument is ours to read or the customer's own parameter.
			var reg *agentcat.Registries
			if instance != nil {
				reg = instance.Registries.Load()
				if reg == nil && instance.RebuildTools != nil {
					func() {
						defer func() {
							if r := recover(); r != nil {
								agentcat.LogRecoveredPanic("mcpgo registry rebuild", r)
							}
						}()
						// Side effect: the list hook stores the registries.
						_, _ = instance.RebuildTools(ctx, nil)
						reg = instance.Registries.Load()
					}()
				}
			}

			// Per-call handle resolution (stateless). Guarded so a panic
			// degrades to an untracked dispatch, never a broken request.
			var (
				resolution agentcat.SessionResolution
				agentID    string
			)
			if tracing {
				var resolved bool
				resolution, agentID, resolved = c.resolveHandles(ctx, request, reg)
				tracing = resolved
			}

			// Strip injected params on a CLONE; the customer's request object
			// is never mutated (CallToolRequest is a value type).
			stripped := stripArgumentsSafely(rawArgs, func() map[string]any {
				return agentcat.StripToolArguments(toolName, rawArgs, reg)
			})
			dispatchReq := request
			if len(stripped) != len(rawArgs) {
				dispatchReq.Params.Arguments = stripped
				setRawArguments(&dispatchReq.Params, rebuildRawArguments(rawArgumentBytes(request), stripped))
			}

			start := time.Now()
			result, err := next(ctx, dispatchReq)
			ms := time.Since(start).Milliseconds()
			if ms > math.MaxInt32 {
				ms = math.MaxInt32
			}
			duration := int32(ms)

			// Wire-only mint-back on a copied result.
			wireResult := result
			if tracing && result != nil {
				func() {
					defer func() {
						if r := recover(); r != nil {
							agentcat.LogRecoveredPanic("mcpgo mint-back", r)
						}
					}()
					wireResult = decorateResult(result, resolution, agentID, toolName, reg)
				}()
			}

			// Which side publishes is decided from state both sides read off
			// the same request, so for the calls that reach here the two are
			// mutually exclusive:
			//
			//   err != nil          → here. handleToolCall turns it into a
			//                         protocol error, so the after-hook never
			//                         runs and OnError cannot see this state.
			//   params.task set     → here. Task-augmented execution (mcp-go's
			//                         detached long-running execution, NOT an
			//                         AgentCat session) runs outside the request
			//                         handler, so no after-hook will ever report
			//                         it; the after-hook stands down on the same
			//                         field.
			//   nil result          → here, while the handles resolved above are
			//                         still in hand: nothing can key a record.
			//   otherwise           → the after-hook, the only seam that sees
			//                         the FINAL result the client receives
			//                         (mcp-go's output validation and any outer
			//                         customer middleware may still replace
			//                         ours after this returns).
			//
			// Calls that never reach this middleware are covered in
			// failure_hooks.go, which also lists the paths outside the Use()
			// seam entirely.
			if tracing {
				func() {
					defer func() {
						if r := recover(); r != nil {
							agentcat.LogRecoveredPanic("mcpgo event capture", r)
						}
					}()
					record := &callRecord{
						resolution: resolution,
						agentID:    agentID,
						duration:   duration,
						original:   result,
					}
					// request.Params.Task is MCP's task augmentation — a detached
					// long-running execution — NOT an AgentCat session. AgentCat
					// has no "task" identifier; every Task here is mcp-go's.
					if err != nil || request.Params.Task != nil || wireResult == nil {
						c.publishCall(ctx, request, result, err, record)
						return
					}
					c.pending.stash(wireResult, record)
				}()
			}

			return wireResult, err
		}
	}
}

// stripArgumentsSafely runs the argument strip under a panic guard, like every
// other seam on the synchronous request path. On panic it returns the raw
// arguments so the call dispatches unmodified — never broken.
func stripArgumentsSafely(rawArgs map[string]any, strip func() map[string]any) (stripped map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			agentcat.LogRecoveredPanic("mcpgo argument stripping", r)
			stripped = rawArgs
		}
	}()
	return strip()
}

// rebuildRawArguments re-renders the dispatched arguments with the injected
// keys removed. mcp-go preserves the original argument bytes precisely so
// handlers using BindArguments/GetRawArguments do not lose integer precision
// above 2^53; dropping keys from the raw object keeps every surviving value's
// bytes verbatim. Falls back to marshaling the stripped map when there are no
// raw bytes (a hand-built request) or they are not a JSON object.
func rebuildRawArguments(raw json.RawMessage, stripped map[string]any) json.RawMessage {
	if len(raw) > 0 {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err == nil {
			for name := range fields {
				if _, keep := stripped[name]; !keep {
					delete(fields, name)
				}
			}
			if cleaned, err := json.Marshal(fields); err == nil {
				return cleaned
			}
		}
	}
	cleaned, err := json.Marshal(stripped)
	if err != nil {
		return nil
	}
	return cleaned
}

// decorateResult applies the text mint-back and structured mirror to a COPY of
// the customer's result. The original result object is what the event records;
// the copy is what goes on the wire.
func decorateResult(
	res *mcp.CallToolResult,
	resolution agentcat.SessionResolution,
	agentID string,
	toolName string,
	reg *agentcat.Registries,
) *mcp.CallToolResult {
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
		cp.Content = append(cp.Content, mcp.NewTextContent(text))
	}

	// (b) Structured mirror: persistent handle state on every response, gated
	// on the output-injection registry, plain-object structuredContent only,
	// customer's own _mcp_instructions key wins.
	mirror := agentcat.BuildHandleMirror(agentcat.MirrorInput{
		Resolution: resolution,
		AgentID:    agentID,
	})
	if mirror != nil && agentcat.ShouldMirror(toolName, reg) {
		if sc, ok := structuredContentAsMap(res); ok {
			if _, customerOwns := sc[agentcat.MCPInstructionsKey]; !customerOwns {
				sc[agentcat.MCPInstructionsKey] = mirror
				cp.StructuredContent = sc
				// The preserved wire bytes would otherwise win at marshal time.
				clearRawStructuredContent(&cp)
			}
		}
	}
	return &cp
}

// undecorateResult returns a COPY of result with AgentCat's own decoration
// removed, or result untouched when none of it is provably ours.
//
// It runs only where the per-call record was lost (the result was replaced, or
// its record was evicted), so the published event still carries the customer's
// original response — the wire-only rule holds on every path. Nothing is
// removed unless it can be rebuilt byte-for-byte from the SDK's own copy
// generators, so customer content is never touched.
func undecorateResult(res *mcp.CallToolResult) *mcp.CallToolResult {
	if res == nil {
		return nil
	}

	content, droppedText := withoutMintBackBlocks(res.Content)
	structured, droppedMirror := withoutHandleMirror(res)
	if !droppedText && !droppedMirror {
		return res
	}

	cp := *res
	if droppedText {
		cp.Content = content
	}
	if droppedMirror {
		cp.StructuredContent = structured
		// The preserved wire bytes would otherwise win at marshal time.
		clearRawStructuredContent(&cp)
	}
	return &cp
}

// withoutMintBackBlocks drops every content block that is provably a mint-back
// block this SDK appended. Position is not assumed: an outer middleware may
// have appended content of its own after ours.
func withoutMintBackBlocks(content []mcp.Content) ([]mcp.Content, bool) {
	dropped := false
	kept := make([]mcp.Content, 0, len(content))
	for _, block := range content {
		var text string
		switch tc := block.(type) {
		case mcp.TextContent:
			text = tc.Text
		case *mcp.TextContent:
			if tc != nil {
				text = tc.Text
			}
		}
		if text != "" && isMintBackText(text) {
			dropped = true
			continue
		}
		kept = append(kept, block)
	}
	if !dropped {
		return content, false
	}
	return kept, true
}

// invalidMintBack is the one block this SDK renders that names no handle, so
// it cannot be recognised by scanning for a ses_ value. Without matching it
// explicitly the correction would survive undecoration and land in the
// published event, breaking the wire-only rule.
var invalidMintBack = agentcat.BuildMintBackText(
	agentcat.SessionResolution{Source: agentcat.SessionSourceInvalid})

// isMintBackText reports whether text is exactly a mint-back block this SDK
// renders — either the correction, or the issued block for the session ID the
// text itself names.
func isMintBackText(text string) bool {
	if text == invalidMintBack {
		return true
	}
	sessionID := sessionIDFromMintBack(text)
	return sessionID != "" && agentcat.BuildMintBackText(
		agentcat.SessionResolution{SessionID: sessionID, Source: agentcat.SessionSourceMinted}) == text
}

// sessionIDFromMintBack extracts the ses_ handle a mint-back block names.
func sessionIDFromMintBack(text string) string {
	start := strings.Index(text, string(agentcat.PrefixSession)+"_")
	if start < 0 {
		return ""
	}
	end := start
	for end < len(text) && isHandleChar(text[end]) {
		end++
	}
	return text[start:end]
}

func isHandleChar(c byte) bool {
	return c == '_' ||
		(c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z')
}

// withoutHandleMirror drops the _mcp_instructions entry when its value is
// provably the mirror this SDK builds. A customer's own value under that key
// is left alone: decorateResult never overwrites one, so it cannot be ours.
func withoutHandleMirror(res *mcp.CallToolResult) (map[string]any, bool) {
	structured, ok := structuredContentAsMap(res)
	if !ok {
		return nil, false
	}
	mirror, ok := structured[agentcat.MCPInstructionsKey].(map[string]any)
	if !ok {
		return nil, false
	}

	sessionID, _ := mirror[agentcat.ParamSessionID].(string)
	agentID, _ := mirror[agentcat.ParamAgentID].(string)
	// Try every resolution this SDK could have mirrored. Enumerating
	// resolutions rather than a minted true/false pair keeps this honest as
	// sources are added: invalid and hook both produce a mirror with no
	// session_id key, and neither is reachable by naming an ID.
	for _, res := range []agentcat.SessionResolution{
		{SessionID: sessionID, Source: agentcat.SessionSourceMinted},
		{SessionID: sessionID, Source: agentcat.SessionSourceSupplied},
		{Source: agentcat.SessionSourceInvalid},
		{SessionID: sessionID, Source: agentcat.SessionSourceHook, HookMode: true},
	} {
		rebuilt := agentcat.BuildHandleMirror(agentcat.MirrorInput{Resolution: res, AgentID: agentID})
		if reflect.DeepEqual(rebuilt, mirror) {
			delete(structured, agentcat.MCPInstructionsKey)
			return structured, true
		}
	}
	return nil, false
}

// structuredContentAsMap returns a COPY of the result's structured content as
// a plain JSON object, or ok=false when it is absent or not an object. Server
// handlers usually store a map or a Go value; RawStructuredContent is only set
// when a result was decoded from the wire, and it wins on re-marshal, so it is
// preferred as the source when present.
func structuredContentAsMap(res *mcp.CallToolResult) (map[string]any, bool) {
	if raw := rawStructuredBytes(res); len(raw) > 0 {
		return decodeJSONObject(raw)
	}
	switch v := res.StructuredContent.(type) {
	case nil:
		return nil, false
	case map[string]any:
		cp := make(map[string]any, len(v)+1)
		for key, val := range v {
			cp[key] = val
		}
		return cp, true
	default:
		// json.RawMessage or an arbitrary marshalable value: the round trip
		// doubles as the copy.
		raw, err := json.Marshal(v)
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

// errorTextFromResult joins the text blocks of an errored tool result. Both the
// value and pointer forms satisfy mcp.Content (isContent has a value receiver),
// and handlers write either.
func errorTextFromResult(result *mcp.CallToolResult) string {
	var msgs []string
	for _, content := range result.Content {
		switch tc := content.(type) {
		case mcp.TextContent:
			msgs = append(msgs, tc.Text)
		case *mcp.TextContent:
			if tc != nil {
				msgs = append(msgs, tc.Text)
			}
		}
	}
	return strings.Join(msgs, " ")
}

// captureToolCallEvent builds and publishes the single mcp:tools/call event.
// duration is nil for failures mcp-go rejected before the handler ran.
func (c *capturer) captureToolCallEvent(
	ctx context.Context,
	request mcp.CallToolRequest,
	result *mcp.CallToolResult,
	callErr error,
	duration *int32,
	resolution agentcat.SessionResolution,
	agentID string,
	userIntent string,
	rawArgs map[string]any,
) {
	opts := c.opts

	isError := callErr != nil
	errorDetails := callErr
	if !isError && result != nil && result.IsError {
		isError = true
		if msg := errorTextFromResult(result); msg != "" {
			errorDetails = fmt.Errorf("%s", msg)
		}
	}

	ec := &agentcat.EventContext{
		ProjectID:       c.projectID,
		SessionID:       resolution.SessionID,
		SessionSource:   string(resolution.Source),
		ServerName:      c.serverName,
		ServerVersion:   c.serverVersion,
		SDKLanguage:     "Go",
		AgentcatVersion: c.agentcatVersion,
		AgentID:         agentID,
	}
	fillClientIdentity(ctx, request, ec)

	// Identify runs on EVERY tool call; the result stamps this event only. An
	// identity without a user ID names no actor, so it is dropped rather than
	// stamping an empty actor.
	if opts.Identify != nil {
		if identity := safeIdentify(ctx, opts, &request); identity != nil && identity.UserID != "" {
			ec.Identity = identity
		}
	}

	evt := agentcat.NewToolCallEvent(ec, duration, isError, errorDetails)
	if evt == nil {
		return
	}
	if userIntent != "" {
		evt.UserIntent = &userIntent
	}
	if request.Params.Name != "" {
		name := request.Params.Name
		evt.ResourceName = &name
	}

	// Raw, unstripped request — exactly what the agent sent. Copied: when no
	// injected params were present the handler received this very map, and the
	// event outlives the request on the publisher's goroutine.
	recorded := make(map[string]any, len(rawArgs))
	for name, value := range rawArgs {
		recorded[name] = value
	}
	evt.Parameters = map[string]any{"name": request.Params.Name, "arguments": recorded}
	if extra := extractExtra(&request); extra != nil {
		evt.Parameters["extra"] = extra
	}
	if result != nil && !isError {
		evt.Response = extractResponse(result)
	}

	// Customer tags/properties first, then SDK tags (SDK wins, cap-exempt).
	attachEventMetadata(ctx, opts, &request, evt)
	agentcat.ApplySDKTags(evt, ec)

	c.publish(evt)
}

// fillClientIdentity resolves client name/version and protocol version per
// request: reserved _meta keys first (mcp-go passes them through untouched),
// then the session's legacy initialize capture. Nothing is cached; transport
// session IDs are ignored entirely.
func fillClientIdentity(ctx context.Context, request mcp.CallToolRequest, ec *agentcat.EventContext) {
	if meta := request.Params.Meta; meta != nil {
		if info, ok := meta.AdditionalFields[agentcat.MetaClientInfoKey].(map[string]any); ok {
			if name, ok := info["name"].(string); ok {
				ec.ClientName = name
			}
			if version, ok := info["version"].(string); ok {
				ec.ClientVersion = version
			}
		}
		if pv, ok := meta.AdditionalFields[agentcat.MetaProtocolVersionKey].(string); ok {
			ec.ProtocolVersion = pv
		}
	}
	if ec.ClientName == "" {
		if session := server.ClientSessionFromContext(ctx); session != nil {
			if withInfo, ok := session.(server.SessionWithClientInfo); ok {
				info := withInfo.GetClientInfo()
				ec.ClientName = info.Name
				ec.ClientVersion = info.Version
			}
		}
	}
}

// safeIdentify invokes the user-supplied Identify callback with panic
// recovery so a faulty callback can never break event capture.
func safeIdentify(ctx context.Context, opts *Options, request any) (identity *agentcat.UserIdentity) {
	defer func() {
		if r := recover(); r != nil {
			agentcat.LogRecoveredPanic("mcpgo Identify callback", r)
			identity = nil
		}
	}()
	return opts.Identify(ctx, request)
}

// getServerInfo reads the MCPServer's unexported name/version fields
// reflectively (mcp-go v0.57.0 exposes no accessor; sanctioned private
// access). Returns copies; the server's own state is never handed out.
func getServerInfo(s *server.MCPServer) (name, version string) {
	defer func() {
		if r := recover(); r != nil {
			name, version = "", ""
		}
	}()
	if s == nil {
		return "", ""
	}
	v := reflect.ValueOf(s).Elem()
	read := func(field string) string {
		f := v.FieldByName(field)
		if !f.IsValid() || f.Kind() != reflect.String {
			return ""
		}
		return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().String()
	}
	return read("name"), read("version")
}
