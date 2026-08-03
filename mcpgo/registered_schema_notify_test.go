package mcpgo

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/v2"
)

// notifySpySession is a minimal initialized ClientSession that records every
// notification the server broadcasts to it. mcp-go only broadcasts to
// initialized sessions (server/session.go:200-208), so this is the smallest
// thing that can observe a tools/list_changed.
type notifySpySession struct {
	id  string
	ch  chan mcp.JSONRPCNotification
	mu  sync.Mutex
	got []string
	//lint:ignore U1000 kept alive by the drain goroutine
	done chan struct{}
}

func newNotifySpySession(t *testing.T, id string) *notifySpySession {
	t.Helper()
	s := &notifySpySession{
		id:   id,
		ch:   make(chan mcp.JSONRPCNotification, 4096),
		done: make(chan struct{}),
	}
	go func() {
		for {
			select {
			case n := <-s.ch:
				s.mu.Lock()
				s.got = append(s.got, n.Method)
				s.mu.Unlock()
			case <-s.done:
				return
			}
		}
	}()
	t.Cleanup(func() { close(s.done) })
	return s
}

func (s *notifySpySession) Initialize()       {}
func (s *notifySpySession) Initialized() bool { return true }
func (s *notifySpySession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return s.ch
}
func (s *notifySpySession) SessionID() string { return s.id }

// count returns how many notifications of the given method have landed.
func (s *notifySpySession) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, got := range s.got {
		if got == method {
			n++
		}
	}
	return n
}

// drainListChanged waits until no new tools/list_changed has arrived for a
// short settle window, then returns the total. Notifications are delivered on
// the server's own goroutine, so absence needs a settle window.
func (s *notifySpySession) drainListChanged() int {
	for {
		before := s.count(mcp.MethodNotificationToolsListChanged)
		settleForAbsentEvents()
		if after := s.count(mcp.MethodNotificationToolsListChanged); after == before {
			return after
		}
	}
}

// TestRepeatedListsDoNotNotifyToolsListChanged is the regression gate for the
// declaration livelock.
//
// A spec-following client re-lists whenever the server sends
// notifications/tools/list_changed. If AgentCat's schema declaration fires a
// notification on every tools/list, that client re-lists, which notifies
// again — a self-sustaining storm that AgentCat, sitting in the customer's
// request path, would be the sole cause of.
//
// The property under test is the NOTIFICATION COUNT across repeated lists, not
// the resulting schema bytes: the bytes are stable whether or not the tools
// are re-registered, which is exactly why the older idempotence test could not
// see this.
//
// Deliberately a PLAIN server: no validation options at all, one tool with a
// declared output schema. mcp-go turns listChanged on by default
// (implicitlyRegisterToolCapabilities, server/server.go:910-915), so this is
// the default configuration, not an exotic one.
func TestRepeatedListsDoNotNotifyToolsListChanged(t *testing.T) {
	mcpServer := server.NewMCPServer("plain", "1.0.0")
	mcpServer.AddTool(
		mcp.NewTool("structured",
			mcp.WithDescription("Returns structuredContent under a declared output schema"),
			mcp.WithOutputSchema[validatedOutput](),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content:           []mcp.Content{mcp.NewTextContent("ok")},
				StructuredContent: map[string]any{"text": "payload"},
			}, nil
		},
	)

	spy := newNotifySpySession(t, "notify-spy")
	if err := mcpServer.RegisterSession(context.Background(), spy); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	instance := newTestInstance(mcpServer, "proj_test", DefaultOptions())
	agentcat.RegisterServer(mcpServer, instance)
	t.Cleanup(func() { unregisterServer(mcpServer) })
	installTracking(mcpServer, instance, DefaultOptions(), (&mockPublisher{}).publish)

	// Whatever Track itself did is the baseline; the property is that serving
	// tools/list adds nothing to it.
	baseline := spy.drainListChanged()

	const lists = 5
	for i := 0; i < lists; i++ {
		listToolRawJSON(t, mcpServer, "structured")
	}

	if got := spy.drainListChanged() - baseline; got != 0 {
		t.Errorf("%d tools/list requests produced %d tools/list_changed notifications; want 0 — a client that re-lists on the notification would loop forever",
			lists, got)
	}
}

// TestRepeatedListsDoNotNotifyUnderSchemaValidation is the same property on the
// server shape that motivated declaring on registered schemas at all, so the
// livelock guard cannot regress only on the configuration the Critical fixed.
func TestRepeatedListsDoNotNotifyUnderSchemaValidation(t *testing.T) {
	mcpServer := server.NewMCPServer("validating", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithInputSchemaValidation(),
		server.WithStrictInputSchemaDefault(),
		server.WithOutputSchemaValidation(),
	)
	registerHandleFixtures(mcpServer)

	spy := newNotifySpySession(t, "notify-spy-validating")
	if err := mcpServer.RegisterSession(context.Background(), spy); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	instance := newTestInstance(mcpServer, "proj_test", DefaultOptions())
	agentcat.RegisterServer(mcpServer, instance)
	t.Cleanup(func() { unregisterServer(mcpServer) })
	installTracking(mcpServer, instance, DefaultOptions(), (&mockPublisher{}).publish)

	baseline := spy.drainListChanged()

	const lists = 5
	for i := 0; i < lists; i++ {
		listToolRawJSON(t, mcpServer, "echo_args")
	}

	if got := spy.drainListChanged() - baseline; got != 0 {
		t.Errorf("%d tools/list requests produced %d tools/list_changed notifications; want 0", lists, got)
	}

	// And the declaration is still in place after all those passes.
	registered := mcpServer.ListTools()["echo_args"]
	schema := marshalRegisteredInputSchema(t, registered.Tool)
	props, _ := schema["properties"].(map[string]any)
	for _, name := range []string{"session_id", "context"} {
		if _, has := props[name]; !has {
			t.Errorf("registered schema lost its %q declaration: %v", name, schema)
		}
	}
}

// TestDeclarationSurvivesToolsAddedAfterTrack pins that the skip does not
// freeze the declaration pass: a tool registered after Track still gets its
// declaration on the next list, and that one list notifies at most because
// mcp-go's own AddTool did.
func TestDeclarationSurvivesToolsAddedAfterTrack(t *testing.T) {
	mcpServer := server.NewMCPServer("late-tools", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithInputSchemaValidation(),
		server.WithStrictInputSchemaDefault(),
	)

	instance := newTestInstance(mcpServer, "proj_test", DefaultOptions())
	agentcat.RegisterServer(mcpServer, instance)
	t.Cleanup(func() { unregisterServer(mcpServer) })
	installTracking(mcpServer, instance, DefaultOptions(), (&mockPublisher{}).publish)

	mcpServer.AddTool(
		mcp.NewTool("late_echo",
			mcp.WithDescription("Echoes the arguments the handler received"),
			mcp.WithString("payload", mcp.Description("Anything at all")),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			encoded, err := json.Marshal(request.GetArguments())
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(string(encoded)), nil
		},
	)

	// First list declares; the call after it must pass validation.
	listToolRawJSON(t, mcpServer, "late_echo")

	result := callToolRaw(t, mcpServer, "late_echo", map[string]any{
		"payload": "keep", "session_id": sid("late"), "context": "why",
	})
	if result.IsError {
		t.Fatalf("a tool added after Track was not declared: %q", resultText(result))
	}
	echoed := decodeEchoedArgs(t, result)
	for _, name := range []string{"session_id", "context"} {
		if _, has := echoed[name]; has {
			t.Errorf("handler must not see %s: %v", name, echoed)
		}
	}
}
