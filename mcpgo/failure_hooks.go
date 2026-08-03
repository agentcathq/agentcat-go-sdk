package mcpgo

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/v2"
)

// MCPServer.Use composes middleware around the tool HANDLER only, so the tool
// middleware is neither the first nor the last thing to touch a tools/call:
//
//   - mcp-go rejects unknown tools (server/server.go:1940), filtered tools
//     (:1946) and schema-invalid arguments (:1991) before the handler runs, so
//     the middleware never sees those calls at all;
//   - after the handler returns, mcp-go may REPLACE the result — output schema
//     validation swaps in an error result (:2021) — and an outer customer
//     middleware may return a different result entirely (middleware applies in
//     reverse, :1998-2001, so AgentCat's is innermost).
//
// The after-hook is therefore the only seam that observes the final result the
// client receives, and it is the sole publisher for calls that complete. The
// middleware publishes only the shapes no after-hook can report (see the
// dispatch comment in call_middleware.go), and OnError publishes the
// pre-handler protocol rejections.
//
// Every tools/call that mcp-go dispatches through handleToolCall publishes
// exactly one event. Four shapes sit outside that guarantee, all of them
// structurally outside the Use() seam and all pre-existing:
//
//   - tools registered with AddTaskTool: their handler is a
//     TaskToolHandlerFunc returning *CreateTaskResult, which Use() middleware
//     cannot wrap at all → no event;
//   - a handler that panics: nothing downstream of it runs → no event;
//   - session-support rejections (METHOD_NOT_FOUND, server/server.go:1955,
//     :1979): not ErrToolNotFound, so OnError stands down → no event;
//   - a handler error that itself wraps server.ErrToolNotFound: the middleware
//     publishes it and OnError cannot tell it from a pre-handler rejection →
//     two events.

// pendingCapacity hard-bounds the per-call records awaiting their after-hook.
// Records normally live microseconds — the after-hook runs on the same
// goroutine as the middleware that stashed one — so this only ever holds
// in-flight calls plus the rare record whose result was replaced before the
// after-hook could claim it. Oldest-first eviction keeps the bound with no
// scanning on the request path.
const pendingCapacity = 256

// callRecord is the per-call state the middleware resolved, handed to the
// after-hook that publishes the event.
type callRecord struct {
	resolution agentcat.SessionResolution
	agentID    string
	duration   int32
	// original is the customer's result BEFORE mint-back decoration; it is
	// what the event records (wire-only discipline).
	original *mcp.CallToolResult
}

// pendingCalls is a bounded map from the result the middleware returned to the
// state it resolved for that call. Keying on the returned pointer is exact
// whenever the result survives to the after-hook; when something replaced it,
// the lookup misses and the after-hook falls back to reporting the request and
// the final result — still exactly one truthful event.
type pendingCalls struct {
	mu      sync.Mutex
	records map[*mcp.CallToolResult]*callRecord
	order   []*mcp.CallToolResult // insertion order, for oldest-first eviction
}

// stash records per-call state for the after-hook, evicting the oldest entry
// when the capacity is reached.
func (p *pendingCalls) stash(result *mcp.CallToolResult, record *callRecord) {
	if result == nil || record == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.records == nil {
		p.records = make(map[*mcp.CallToolResult]*callRecord, pendingCapacity)
	}
	if _, exists := p.records[result]; !exists {
		// Oldest-first eviction. Claimed entries leave a tombstone here, so
		// most evictions just drop a key that is already gone — which keeps
		// both stash and claim O(1) with no scanning.
		for len(p.order) >= pendingCapacity {
			oldest := p.order[0]
			p.order = p.order[1:]
			delete(p.records, oldest)
		}
		p.order = append(p.order, result)
	}
	p.records[result] = record
}

// claim removes and returns the state stashed for a result, or nil when the
// result carries none (it was replaced, or the middleware never ran).
func (p *pendingCalls) claim(result *mcp.CallToolResult) *callRecord {
	if result == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	record, ok := p.records[result]
	if !ok {
		return nil
	}
	delete(p.records, result)
	return record
}

// registerFailureHooks wires the two hooks that publish tool-call events
// outside the middleware. Both are filtered strictly to tools/call; no other
// method publishes anything.
func registerFailureHooks(hooks *server.Hooks, c *capturer) {
	// Pre-handler rejections that surface as protocol errors: unknown tool and
	// tool-filter rejection, both of which wrap ErrToolNotFound. Handler
	// errors also land here, but the middleware publishes those (it holds the
	// resolution and duration this hook cannot recover).
	hooks.AddOnError(func(ctx context.Context, id any, method mcp.MCPMethod, message any, err error) {
		defer func() {
			if r := recover(); r != nil {
				agentcat.LogRecoveredPanic("mcpgo failure capture", r)
			}
		}()
		if method != mcp.MethodToolsCall || !c.servesRequest(ctx) {
			return
		}
		request, ok := message.(*mcp.CallToolRequest)
		if !ok || request == nil {
			return
		}
		// ErrToolNotFound is only produced before the handler runs, so this
		// cannot double-publish an error the middleware already captured.
		if !errors.Is(err, server.ErrToolNotFound) {
			return
		}
		c.publishCall(ctx, *request, nil, err, nil)
	})

	// Every tools/call that completed without a protocol error, whether or not
	// the middleware ran and whether or not its result survived.
	hooks.AddAfterCallTool(func(ctx context.Context, id any, message *mcp.CallToolRequest, result any) {
		defer func() {
			if r := recover(); r != nil {
				agentcat.LogRecoveredPanic("mcpgo call capture", r)
			}
		}()
		if message == nil || !c.servesRequest(ctx) {
			return
		}
		toolResult, _ := result.(*mcp.CallToolResult)
		record := c.pending.claim(toolResult)

		// Both of these are published by the middleware, which holds state
		// this hook cannot recover: task-augmented calls (this hook only ever
		// sees the CreateTaskResult) and handlers that returned no result.
		//
		// Params.Task is MCP's task augmentation, NOT an AgentCat session.
		if message.Params.Task != nil || toolResult == nil {
			return
		}

		if record != nil {
			// The middleware's result survived to the client: report the
			// customer's undecorated original with the handles it resolved.
			c.publishCall(ctx, *message, record.original, nil, record)
			return
		}
		// No record: either the call never reached the middleware (an input
		// schema rejection) or something replaced its result — possibly after
		// the record was evicted. The client received THIS result, so it is
		// what the event reports, with any decoration of ours taken back off.
		warnRecordLost(message.Params.Name)
		c.publishCall(ctx, *message, undecorateResult(toolResult), nil, nil)
	})
}

// warnRecordLost reports that a completed tools/call is about to be published
// without the per-call state the middleware resolved.
//
// If the middleware DID run for this call — its result was replaced by
// mcp-go's output-schema validation or by an outer middleware, or its record
// was evicted under load — then handles get resolved a second time here: the
// event can be filed under a different session than the one the agent already
// received on the wire, and a customer's ResolveSessionID hook runs twice for one
// call. mcp-go offers no seam to carry the first resolution across: hooks
// cannot modify the context (and the after-hook's context is created upstream
// of the tool middleware anyway), and the *mcp.CallToolRequest the hook
// receives is the request handler's own local, not the copy the middleware
// saw. Warning is therefore the most this seam can do.
//
// The same branch also covers calls that never reached the middleware at all,
// where re-resolving is simply the first and only resolution and costs
// nothing — which is why this is a warning and not an error.
func warnRecordLost(toolName string) {
	agentcat.LogWarn("mcpgo: publishing tools/call %q without the middleware's per-call state (the result was replaced, the record was evicted, or the call was rejected before the handler). If the middleware ran, handles resolve a second time here: this event's session may differ from the one the agent received, and a ResolveSessionID hook runs twice for this call.",
		toolName)
}

// publishCall builds and publishes the single event for one tool call.
// record carries the middleware's resolved handles and duration when it ran;
// without it the handles are resolved from the request and no duration is
// recorded (nothing of ours timed the call).
func (c *capturer) publishCall(
	ctx context.Context,
	request mcp.CallToolRequest,
	result *mcp.CallToolResult,
	callErr error,
	record *callRecord,
) {
	if !c.tracingEnabled() {
		return
	}

	var (
		resolution agentcat.SessionResolution
		agentID    string
		duration   *int32
	)
	if record != nil {
		resolution, agentID = record.resolution, record.agentID
		duration = &record.duration
	} else {
		// No record. See warnRecordLost for what that costs when the
		// middleware did run for this call. loadRegistries deliberately does
		// not rebuild — an error path must not drive a synthetic tools/list.
		var ok bool
		if resolution, agentID, ok = c.resolveHandles(ctx, request, c.loadRegistries()); !ok {
			return
		}
	}

	if callErr == nil && result == nil {
		callErr = fmt.Errorf("tools/call %q returned no result", request.Params.Name)
	}

	rawArgs := request.GetArguments()
	c.captureToolCallEvent(ctx, request, result, callErr, duration,
		resolution, agentID, c.userIntent(rawArgs), rawArgs)
}
