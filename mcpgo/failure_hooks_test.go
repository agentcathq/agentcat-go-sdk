package mcpgo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/v2"
)

// newFailureServer builds a server whose tool-call failures cover every
// pre-handler and handler path: an unknown tool (never registered), a tool
// whose schema rejects the call before the handler runs, a handler that
// returns a Go error, and a handler that returns an IsError result.
func newFailureServer(t *testing.T) *server.MCPServer {
	t.Helper()

	mcpServer := server.NewMCPServer("failure-server", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithInputSchemaValidation(),
	)

	mcpServer.AddTool(
		mcp.NewTool("strict_tool",
			mcp.WithDescription("Requires a string count"),
			mcp.WithString("count", mcp.Required(), mcp.Description("A string")),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	)
	mcpServer.AddTool(
		mcp.NewTool("handler_errors", mcp.WithDescription("Returns a Go error")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, errors.New("handler exploded")
		},
	)
	mcpServer.AddTool(
		mcp.NewTool("result_errors", mcp.WithDescription("Returns an IsError result")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultError("tool said no"), nil
		},
	)
	return mcpServer
}

// callFailing drives a tools/call that is expected to fail and returns every
// tool-call event published for it.
func callFailing(t *testing.T, h *spyHarness, name string, args map[string]any) []*agentcat.Event {
	t.Helper()

	before := len(filterEvents(h.Mock.getEvents(), "mcp:tools/call"))

	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	_, _ = h.Client.CallTool(context.Background(), req)

	events := filterEvents(h.Mock.getEvents(), "mcp:tools/call")
	return events[before:]
}

// TestFailedCallsPublishExactlyOneEvent covers the three failure shapes the
// call path can take. Pre-handler failures never reach the tool middleware —
// mcp-go composes Use() middleware around the handler only — so they are
// captured from hooks; handler failures are captured by the middleware. Each
// must publish exactly one event, never zero and never two.
func TestFailedCallsPublishExactlyOneEvent(t *testing.T) {
	cases := []struct {
		name     string
		tool     string
		args     map[string]any
		wantText string
	}{
		{"unknown tool", "no_such_tool", map[string]any{"session_id": sid("f")}, "not found"},
		{"input validation rejection", "strict_tool", map[string]any{"count": 7, "session_id": sid("f")}, "count"},
		{"handler returns an error", "handler_errors", map[string]any{"session_id": sid("f")}, "handler exploded"},
		{"handler returns an error result", "result_errors", map[string]any{"session_id": sid("f")}, "tool said no"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newSpyHarnessOn(t, newFailureServer(t), nil, "proj_test")

			events := callFailing(t, h, tc.tool, tc.args)

			if len(events) != 1 {
				t.Fatalf("expected exactly 1 event for a failed call, got %d", len(events))
			}
			evt := events[0]
			if evt.IsError == nil || !*evt.IsError {
				t.Error("the event must be marked as an error")
			}
			if evt.GetSessionId() != sid("f") {
				t.Errorf("the supplied session must attribute the failure, got %q", evt.GetSessionId())
			}
			if evt.ResourceName == nil || *evt.ResourceName != tc.tool {
				t.Errorf("resource name = %v, want %q", evt.ResourceName, tc.tool)
			}
			if evt.Error == nil {
				t.Fatal("the event must carry error details")
			}
			if msg, _ := evt.Error["message"].(string); !strings.Contains(msg, tc.wantText) {
				t.Errorf("error message %q does not mention %q", msg, tc.wantText)
			}
			if evt.Tags == nil || (*evt.Tags)["agentcat_session_id_source"] != "supplied" {
				t.Errorf("SDK tags missing on the failure event: %v", evt.Tags)
			}
		})
	}
}

// TestOutputValidationFailurePublishesOneTruthfulEvent pins the call whose
// result mcp-go REPLACES after the middleware ran: output schema validation
// swaps the handler's result for an error result the client actually receives.
// Exactly one event may be published, and it must describe that error — never
// a success the client never saw.
func TestOutputValidationFailurePublishesOneTruthfulEvent(t *testing.T) {
	mcpServer := server.NewMCPServer("output-validating-server", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithOutputSchemaValidation(),
	)
	mcpServer.AddTool(
		mcp.NewTool("violates_schema",
			mcp.WithDescription("Returns structured content its schema forbids"),
			mcp.WithOutputSchema[validatedOutput](),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content:           []mcp.Content{mcp.NewTextContent("ok")},
				StructuredContent: map[string]any{"unexpected": true},
			}, nil
		},
	)

	h := newSpyHarnessOn(t, mcpServer, nil, "proj_test")

	before := len(filterEvents(h.Mock.getEvents(), "mcp:tools/call"))
	req := mcp.CallToolRequest{}
	req.Params.Name = "violates_schema"
	req.Params.Arguments = map[string]any{"session_id": sid("v")}
	result, err := h.Client.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatalf("fixture must fail output validation, got %q", resultText(result))
	}

	events := filterEvents(h.Mock.getEvents(), "mcp:tools/call")[before:]
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.IsError == nil || !*evt.IsError {
		t.Error("the event must report the error the client received, not a fabricated success")
	}
	if evt.Response != nil {
		if isErr, ok := evt.Response["isError"].(bool); ok && !isErr {
			t.Errorf("the event must not carry a response the client never received: %v", evt.Response)
		}
	}
}

// TestCustomerMiddlewareReplacementPublishesOneEvent pins the other way a
// result gets substituted: MCPServer.Use applies middleware in reverse, so a
// customer middleware registered before Track wraps AgentCat's and can return
// a completely different result.
func TestCustomerMiddlewareReplacementPublishesOneEvent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		replacement func() *mcp.CallToolResult
		wantIsError bool
		wantText    string
	}{
		{
			name:        "error replacement",
			replacement: func() *mcp.CallToolResult { return mcp.NewToolResultError("replaced by customer middleware") },
			wantIsError: true,
			wantText:    "replaced by customer middleware",
		},
		{
			name:        "success replacement",
			replacement: func() *mcp.CallToolResult { return mcp.NewToolResultText("customer replacement") },
			wantIsError: false,
			wantText:    "customer replacement",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mcpServer, _ := CreateTodoServerSimple()

			// Registered BEFORE Track, so this middleware is the outer one.
			mcpServer.Use(func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
				return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					if _, err := next(ctx, request); err != nil {
						return nil, err
					}
					return tc.replacement(), nil
				}
			})

			h := newSpyHarnessOn(t, mcpServer, nil, "proj_test")

			before := len(filterEvents(h.Mock.getEvents(), "mcp:tools/call"))
			req := mcp.CallToolRequest{}
			req.Params.Name = "add_todo"
			req.Params.Arguments = map[string]any{"title": "x", "session_id": sid("m")}
			result, err := h.Client.CallTool(context.Background(), req)
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if !strings.Contains(resultText(result), tc.wantText) {
				t.Fatalf("customer middleware did not replace the result: %q", resultText(result))
			}

			events := filterEvents(h.Mock.getEvents(), "mcp:tools/call")[before:]
			if len(events) != 1 {
				t.Fatalf("expected exactly 1 event, got %d", len(events))
			}
			gotError := events[0].IsError != nil && *events[0].IsError
			if gotError != tc.wantIsError {
				t.Errorf("event isError = %v, want %v — the event must describe the result the client received",
					gotError, tc.wantIsError)
			}
			if !tc.wantIsError && !strings.Contains(fmt.Sprint(events[0].Response), tc.wantText) {
				t.Errorf("event response %v must describe the result the client received (%q)",
					events[0].Response, tc.wantText)
			}
		})
	}
}

// TestTaskAugmentedCallsPublishExactlyOneEvent pins the other half of the
// publisher partition: calls carrying params.task are published by the
// middleware (task execution runs detached from the request handler, so no
// after-hook reports it) and the after-hook stands down for them. Both a real
// task-augmented execution and a task param on a tool that does not support
// tasks — which mcp-go runs synchronously — must publish exactly once.
//
// "Task" here is MCP's task augmentation, not an AgentCat session; the two
// travel together on this call and must not be confused.
func TestTaskAugmentedCallsPublishExactlyOneEvent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		taskSupport bool
	}{
		{"tool supports tasks", true},
		{"tool ignores the task param", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mcpServer := server.NewMCPServer("task-server", "1.0.0", server.WithToolCapabilities(true))

			opts := []mcp.ToolOption{mcp.WithDescription("A tool that may run as a task")}
			if tc.taskSupport {
				opts = append(opts, mcp.WithTaskSupport(mcp.TaskSupportOptional))
			}
			mcpServer.AddTool(
				mcp.NewTool("maybe_task", opts...),
				func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					return mcp.NewToolResultText("task done"), nil
				},
			)

			h := newSpyHarnessOn(t, mcpServer, nil, "proj_test")

			before := len(filterEvents(h.Mock.getEvents(), "mcp:tools/call"))
			// Driven as raw JSON: a task-augmented call answers with a
			// CreateTaskResult, which the typed client cannot decode.
			h.Server.HandleMessage(context.Background(), []byte(
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+
					`{"name":"maybe_task","arguments":{"session_id":"`+sid("t")+`"},"task":{}}}`))

			events := filterEvents(h.Mock.waitForEvents(before+1, 3*time.Second), "mcp:tools/call")[before:]
			if len(events) != 1 {
				t.Fatalf("expected exactly 1 event, got %d", len(events))
			}
			evt := events[0]
			if evt.GetSessionId() != sid("t") {
				t.Errorf("supplied session must attribute the event, got %q", evt.GetSessionId())
			}
			// The event must describe the tool's real outcome, not the
			// task envelope the request handler answered with.
			if evt.IsError != nil && *evt.IsError {
				t.Errorf("the tool succeeded; the event must not report an error: %v", evt.Error)
			}
			if !strings.Contains(fmt.Sprint(evt.Response), "task done") {
				t.Errorf("event response %v must describe the tool's result", evt.Response)
			}
		})
	}
}

// TestEvictedRecordPublishesUndecoratedResponse pins the wire-only constraint
// on the eviction path. The per-call record is held in a bounded map, so a
// burst of calls inside one call's stash→claim window can evict its record.
// The event published on that miss must still carry the customer's ORIGINAL
// response — never AgentCat's decorated copy.
func TestEvictedRecordPublishesUndecoratedResponse(t *testing.T) {
	mcpServer, _ := CreateTodoServerSimple()
	registerHandleFixtures(mcpServer)

	// Registered before Track, so this middleware wraps AgentCat's: by the
	// time it floods, the call under test has already stashed its record.
	var flooding atomic.Bool
	mcpServer.Use(func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := next(ctx, request)
			if request.Params.Name == "structured_only" && flooding.CompareAndSwap(false, true) {
				for range pendingCapacity + 8 {
					mcpServer.HandleMessage(ctx, []byte(
						`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"list_todos","arguments":{}}}`))
				}
				flooding.Store(false)
			}
			return result, err
		}
	})

	h := newSpyHarnessOn(t, mcpServer, nil, "proj_test")
	h.listTools() // so the mirror registry exists and structured_only mirrors

	req := mcp.CallToolRequest{}
	req.Params.Name = "structured_only"
	req.Params.Arguments = map[string]any{}
	res, err := h.Client.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	// Precondition: the wire result really is decorated.
	if !strings.Contains(firstText(t, res), "session_id issued") {
		t.Fatalf("fixture must produce a decorated wire result: %q", firstText(t, res))
	}

	var evt *agentcat.Event
	for _, candidate := range filterEvents(h.Mock.getEvents(), "mcp:tools/call") {
		if name, _ := candidate.Parameters["name"].(string); name == "structured_only" {
			evt = candidate
		}
	}
	if evt == nil {
		t.Fatal("no event published for structured_only")
	}

	response := fmt.Sprint(evt.Response)
	if strings.Contains(response, "[session_id") {
		t.Errorf("mint-back leaked into the published event response: %s", response)
	}
	if strings.Contains(response, "mcp_session") {
		t.Errorf("mirror leaked into the published event response: %s", response)
	}
	if !strings.Contains(response, "structured") {
		t.Errorf("the customer's own payload must survive: %s", response)
	}
}

// TestSharedHooksAcrossServersAttributeCorrectly pins that a customer may pass
// one *server.Hooks value to two tracked servers: each call publishes exactly
// one event, attributed to the server that served it.
func TestSharedHooksAcrossServersAttributeCorrectly(t *testing.T) {
	shared := &server.Hooks{}

	serverA, _ := CreateTodoServerSimple(server.WithHooks(shared))
	serverB := server.NewMCPServer("server-b", "2.0.0",
		server.WithToolCapabilities(true),
		server.WithHooks(shared),
	)
	serverB.AddTool(
		mcp.NewTool("only_b", mcp.WithDescription("Lives on server B")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("from b"), nil
		},
	)

	instanceA := newTestInstance(serverA, "proj_a", DefaultOptions())
	agentcat.RegisterServer(serverA, instanceA)
	t.Cleanup(func() { unregisterServer(serverA) })
	mockA := &mockPublisher{}
	installTracking(serverA, instanceA, DefaultOptions(), mockA.publish)

	instanceB := newTestInstance(serverB, "proj_b", DefaultOptions())
	agentcat.RegisterServer(serverB, instanceB)
	t.Cleanup(func() { unregisterServer(serverB) })
	mockB := &mockPublisher{}
	installTracking(serverB, instanceB, DefaultOptions(), mockB.publish)

	callToolRaw(t, serverA, "add_todo", map[string]any{"title": "a"})
	callToolRaw(t, serverB, "only_b", map[string]any{})

	eventsA := filterEvents(mockA.getEvents(), "mcp:tools/call")
	eventsB := filterEvents(mockB.getEvents(), "mcp:tools/call")

	if len(eventsA) != 1 {
		t.Fatalf("server A published %d events, want 1", len(eventsA))
	}
	if len(eventsB) != 1 {
		t.Fatalf("server B published %d events, want 1", len(eventsB))
	}
	if eventsA[0].ProjectId != "proj_a" || *eventsA[0].ServerName != "todo-server" {
		t.Errorf("server A event misattributed: project=%q server=%v", eventsA[0].ProjectId, eventsA[0].ServerName)
	}
	if eventsB[0].ProjectId != "proj_b" || *eventsB[0].ServerName != "server-b" {
		t.Errorf("server B event misattributed: project=%q server=%v", eventsB[0].ProjectId, eventsB[0].ServerName)
	}
	if name, _ := eventsA[0].Parameters["name"].(string); name != "add_todo" {
		t.Errorf("server A recorded the wrong call: %v", eventsA[0].Parameters)
	}
	if name, _ := eventsB[0].Parameters["name"].(string); name != "only_b" {
		t.Errorf("server B recorded the wrong call: %v", eventsB[0].Parameters)
	}
}

// TestSharedHooksAreInstrumentedExactlyOnce pins the O(requests) growth bug:
// AgentCat's hook closures dispatch on the request context, so tracking N
// servers that share one *server.Hooks value must append its three closures
// once, not N times — in the per-request factory topology every extra trio
// outlives its server on the customer's long-lived Hooks value and every
// request iterates all of them.
func TestSharedHooksAreInstrumentedExactlyOnce(t *testing.T) {
	shared := &server.Hooks{}

	type tracked struct {
		server *server.MCPServer
		mock   *mockPublisher
		proj   string
		tool   string
	}
	servers := make([]tracked, 0, 3)
	for i, proj := range []string{"proj_once_a", "proj_once_b", "proj_once_c"} {
		tool := fmt.Sprintf("tool_%d", i)
		s := server.NewMCPServer(fmt.Sprintf("shared-%d", i), "1.0.0",
			server.WithToolCapabilities(true),
			server.WithHooks(shared),
		)
		s.AddTool(
			mcp.NewTool(tool, mcp.WithDescription("per-server tool")),
			func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultText("ok"), nil
			},
		)
		instance := newTestInstance(s, proj, DefaultOptions())
		agentcat.RegisterServer(s, instance)
		t.Cleanup(func() { unregisterServer(s) })
		mock := &mockPublisher{}
		installTracking(s, instance, DefaultOptions(), mock.publish)

		if got := len(shared.OnAfterListTools); got != 1 {
			t.Fatalf("after install %d: %d AfterListTools hooks, want 1", i+1, got)
		}
		if got := len(shared.OnError); got != 1 {
			t.Fatalf("after install %d: %d OnError hooks, want 1", i+1, got)
		}
		if got := len(shared.OnAfterCallTool); got != 1 {
			t.Fatalf("after install %d: %d AfterCallTool hooks, want 1", i+1, got)
		}
		servers = append(servers, tracked{server: s, mock: mock, proj: proj, tool: tool})
	}

	// The single closure trio still serves every server: one event each,
	// attributed to the server that took the call.
	for _, tr := range servers {
		callToolRaw(t, tr.server, tr.tool, map[string]any{})
	}
	for _, tr := range servers {
		events := filterEvents(tr.mock.getEvents(), "mcp:tools/call")
		if len(events) != 1 {
			t.Fatalf("%s published %d events, want 1", tr.proj, len(events))
		}
		if events[0].ProjectId != tr.proj {
			t.Errorf("event misattributed: got project %q, want %q", events[0].ProjectId, tr.proj)
		}
	}
}

// TestNilResultCallResolvesHandlesOnce pins that a handler returning no result
// at all still resolves its handles exactly once: the customer's ResolveSessionID
// hook runs on every tool call, but never twice for the same call.
func TestNilResultCallResolvesHandlesOnce(t *testing.T) {
	var resolves atomic.Int32

	mcpServer := server.NewMCPServer("nil-result-server", "1.0.0", server.WithToolCapabilities(true))
	mcpServer.AddTool(
		mcp.NewTool("returns_nothing", mcp.WithDescription("Returns neither a result nor an error")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, nil
		},
	)

	opts := &Options{
		ResolveSessionID: func(ctx context.Context, request mcp.CallToolRequest) (string, error) {
			resolves.Add(1)
			return "customer-abc", nil
		},
	}
	h := newSpyHarnessOn(t, mcpServer, opts, "proj_1")

	// The typed client cannot decode a null result; drive the raw message.
	h.Server.HandleMessage(context.Background(), []byte(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"returns_nothing","arguments":{}}}`))

	if got := resolves.Load(); got != 1 {
		t.Errorf("ResolveSessionID ran %d times for one call, want 1", got)
	}
	events := filterEvents(h.Mock.getEvents(), "mcp:tools/call")
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(events))
	}
	if events[0].GetSessionId() != "ses_2cOHEO0LYGADMzRvWTXXVbbgxgm" {
		t.Errorf("event session = %q, want the hook-derived handle", events[0].GetSessionId())
	}
}

// TestSuccessfulCallsPublishExactlyOneEvent is the negative-space pin for the
// failure hooks: adding them must not double-publish successful calls.
func TestSuccessfulCallsPublishExactlyOneEvent(t *testing.T) {
	h := newSpyHarnessOn(t, newFailureServer(t), nil, "proj_test")

	before := len(filterEvents(h.Mock.getEvents(), "mcp:tools/call"))
	req := mcp.CallToolRequest{}
	req.Params.Name = "strict_tool"
	req.Params.Arguments = map[string]any{"count": "7", "session_id": sid("ok")}
	result, err := h.Client.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("call should have succeeded: %q", resultText(result))
	}

	events := filterEvents(h.Mock.getEvents(), "mcp:tools/call")[before:]
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event for a successful call, got %d", len(events))
	}
	if events[0].IsError != nil && *events[0].IsError {
		t.Error("a successful call must not be recorded as an error")
	}
}

// TestFailureHooksOnlyPublishToolCalls pins that the restored hooks are
// filtered strictly: a failing resource read or prompt fetch publishes nothing.
func TestFailureHooksOnlyPublishToolCalls(t *testing.T) {
	h := newSpyHarness(t, nil)
	ctx := context.Background()

	readReq := mcp.ReadResourceRequest{}
	readReq.Params.URI = "todo://does-not-exist"
	_, _ = h.Client.ReadResource(ctx, readReq)

	promptReq := mcp.GetPromptRequest{}
	promptReq.Params.Name = "no_such_prompt"
	_, _ = h.Client.GetPrompt(ctx, promptReq)

	if events := h.Mock.getEvents(); len(events) != 0 {
		t.Errorf("only tool calls may publish, got %v", eventTypes(events))
	}
}

// TestFailedCallsPublishNothingWhenTracingDisabled pins that the failure hooks
// honour DisableTracing like every other capture path.
func TestFailedCallsPublishNothingWhenTracingDisabled(t *testing.T) {
	h := newSpyHarnessOn(t, newFailureServer(t), &Options{DisableTracing: true}, "proj_test")

	callFailing(t, h, "no_such_tool", map[string]any{})
	callFailing(t, h, "strict_tool", map[string]any{"count": 7})

	if events := h.Mock.getEvents(); len(events) != 0 {
		t.Errorf("expected 0 events with DisableTracing, got %v", eventTypes(events))
	}
}

// TestUnknownToolFailureMintsWhenNoHandleSupplied pins that a failure without a
// supplied handle still gets a session, so the event is never unattributed.
func TestUnknownToolFailureMintsWhenNoHandleSupplied(t *testing.T) {
	h := newSpyHarnessOn(t, newFailureServer(t), nil, "proj_test")

	events := callFailing(t, h, "no_such_tool", map[string]any{})
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(events))
	}
	if !strings.HasPrefix(events[0].GetSessionId(), "ses_") {
		t.Errorf("failure event must carry a session, got %q", events[0].GetSessionId())
	}
	if (*events[0].Tags)["agentcat_session_id_source"] != "minted" {
		t.Errorf("session source = %v, want minted", *events[0].Tags)
	}
}
