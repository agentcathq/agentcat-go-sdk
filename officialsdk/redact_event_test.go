package officialsdk

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	agentcat "go.agentcat.com/sdk"
)

// captureAPI is a stand-in for the AgentCat API that records raw request
// bodies so tests can assert on exactly what would have been published.
type captureAPI struct {
	mu     sync.Mutex
	bodies []string
	srv    *httptest.Server
}

func newCaptureAPI(t *testing.T) *captureAPI {
	t.Helper()
	c := &captureAPI{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies = append(c.bodies, string(body))
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *captureAPI) waitFor(timeout time.Duration, predicate func(bodies []string) bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		ok := predicate(append([]string(nil), c.bodies...))
		c.mu.Unlock()
		if ok {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func (c *captureAPI) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.bodies...)
}

// setupTrackedServer creates a real tracked server (real publisher, no mock)
// connected over in-memory transports, with the global publisher reset so
// this test's options take effect.
func setupTrackedServer(t *testing.T, opts *Options) *mcp.ClientSession {
	t.Helper()

	_ = agentcat.Shutdown(context.Background())
	t.Cleanup(func() {
		_ = agentcat.Shutdown(context.Background())
	})

	server := mcp.NewServer(&mcp.Implementation{Name: "redact-test-server", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "greet",
		Description: "Greets a person by name",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args greetArgs) (*mcp.CallToolResult, greetResult, error) {
		return nil, greetResult{Text: "Hello, " + args.Name + "!"}, nil
	})

	if _, err := Track(server, "test_project", opts); err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	t.Cleanup(func() {
		agentcat.UnregisterServer(server)
	})

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "redact-test-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		clientSession.Close()
		serverSession.Wait()
	})

	return clientSession
}

// TestRedactEvent_HookRewritesEventBeforePublish verifies that the RedactEvent
// hook receives the full event with raw values and that its rewrite is what
// reaches the API.
func TestRedactEvent_HookRewritesEventBeforePublish(t *testing.T) {
	api := newCaptureAPI(t)

	clientSession := setupTrackedServer(t, &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		APIBaseURL:             api.srv.URL,
		RedactEvent: func(event *agentcat.Event) (*agentcat.Event, error) {
			if event.GetEventType() == "mcp:tools/call" {
				modified := *event
				modified.Parameters = map[string]any{"replaced": true}
				modified.Response = nil // the tool echoes its arguments back
				return &modified, nil
			}
			return event, nil
		},
	})

	result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "Secret Name"},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected tool call to succeed")
	}

	found := api.waitFor(3*time.Second, func(bodies []string) bool {
		for _, b := range bodies {
			if strings.Contains(b, "mcp:tools/call") {
				return true
			}
		}
		return false
	})
	if !found {
		t.Fatal("no mcp:tools/call event reached the capture API")
	}

	for _, b := range api.snapshot() {
		if strings.Contains(b, "mcp:tools/call") {
			if !strings.Contains(b, `"replaced":true`) {
				t.Errorf("published tool-call event does not contain the hook's rewrite: %s", b)
			}
			if strings.Contains(b, "Secret Name") {
				t.Errorf("raw parameters leaked past the RedactEvent hook: %s", b)
			}
		}
	}
}

// TestRedactEvent_NilDropsEvent verifies that returning nil from the hook
// drops the event entirely while other event types still publish and the
// server continues to operate.
func TestRedactEvent_NilDropsEvent(t *testing.T) {
	api := newCaptureAPI(t)

	clientSession := setupTrackedServer(t, &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		APIBaseURL:             api.srv.URL,
		RedactEvent: func(event *agentcat.Event) (*agentcat.Event, error) {
			if event.GetEventType() == "mcp:tools/call" {
				return nil, nil // drop
			}
			return event, nil
		},
	})

	ctx := context.Background()
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "Dropped"},
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected tool call to succeed")
	}

	arrived := api.waitFor(3*time.Second, func(bodies []string) bool {
		return len(bodies) > 0
	})
	if !arrived {
		t.Fatal("no events at all reached the capture API")
	}
	time.Sleep(500 * time.Millisecond) // settle: give a dropped event time to (not) arrive

	for _, b := range api.snapshot() {
		if strings.Contains(b, "mcp:tools/call") {
			t.Errorf("tool-call event reached the API despite nil return: %s", b)
		}
	}

	// The server is still fully operational.
	result2, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "Again"},
	})
	if err != nil || result2.IsError {
		t.Fatalf("second CallTool failed: err=%v", err)
	}
}

// TestRedactEvent_PanicInHookDoesNotCrashServer verifies that a panic inside
// the RedactEvent hook drops the event but never crashes the MCP server.
func TestRedactEvent_PanicInHookDoesNotCrashServer(t *testing.T) {
	clientSession := setupTrackedServer(t, &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		RedactEvent: func(event *agentcat.Event) (*agentcat.Event, error) {
			panic("redact event panic!")
		},
	})

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name:      "greet",
			Arguments: map[string]any{"name": "World"},
		})
		if err != nil || result.IsError {
			t.Fatalf("CallTool %d failed despite hook panic: err=%v", i, err)
		}
	}
}
