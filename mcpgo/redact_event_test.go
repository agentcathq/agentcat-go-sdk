package mcpgo

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
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

// waitFor polls until predicate returns true or the timeout elapses.
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

// resetPublisher shuts down the global publisher so the next Track call
// creates a fresh one carrying this test's hooks and API base URL.
func resetPublisher(t *testing.T) {
	t.Helper()
	_ = agentcat.Shutdown(context.Background())
	t.Cleanup(func() {
		_ = agentcat.Shutdown(context.Background())
	})
}

func callToolHTTP(t *testing.T, c interface {
	CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
}, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	result, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool(%s) failed: %v", name, err)
	}
	return result
}

// TestRedactEvent_HookRewritesEventBeforePublish verifies over a real HTTP
// transport that the RedactEvent hook receives the full event with raw values
// and that its rewrite is what reaches the API.
func TestRedactEvent_HookRewritesEventBeforePublish(t *testing.T) {
	resetPublisher(t)
	api := newCaptureAPI(t)

	mcpClient := setupStreamableHTTP(t, &Options{
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

	result := callToolHTTP(t, mcpClient, "add_todo", map[string]any{
		"title":       "Secret task title",
		"description": "Sensitive description",
	})
	assertContains(t, resultText(result), "Added todo")

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
			if strings.Contains(b, "Secret task title") {
				t.Errorf("raw parameters leaked past the RedactEvent hook: %s", b)
			}
		}
	}
}

// TestRedactEvent_NilDropsEvent verifies over a real HTTP transport that
// returning nil from the hook drops the event entirely while other event
// types still publish and the server continues to operate.
func TestRedactEvent_NilDropsEvent(t *testing.T) {
	resetPublisher(t)
	api := newCaptureAPI(t)

	mcpClient := setupStreamableHTTP(t, &Options{
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

	result := callToolHTTP(t, mcpClient, "add_todo", map[string]any{
		"title":       "Should be dropped",
		"description": "This event never reaches the API",
	})
	assertContains(t, resultText(result), "Added todo")

	// Non-tool-call events (e.g. initialize) still flow, proving the pipeline
	// is alive while tool-call events are dropped.
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
	listResult := callToolHTTP(t, mcpClient, "list_todos", map[string]any{})
	assertContains(t, resultText(listResult), "Should be dropped")
}

// TestRedactEvent_PanicInHookDoesNotCrashServer verifies that a panic inside
// the RedactEvent hook drops the event but never crashes the MCP server.
func TestRedactEvent_PanicInHookDoesNotCrashServer(t *testing.T) {
	resetPublisher(t)

	mcpClient := setupStreamableHTTP(t, &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		RedactEvent: func(event *agentcat.Event) (*agentcat.Event, error) {
			panic("redact event panic!")
		},
	})

	result1 := callToolHTTP(t, mcpClient, "add_todo", map[string]any{
		"title":       "Secret task",
		"description": "Contains sensitive data",
	})
	assertContains(t, resultText(result1), "Added todo")

	// Second call — the server is still fully operational.
	result2 := callToolHTTP(t, mcpClient, "list_todos", map[string]any{})
	assertContains(t, resultText(result2), "Secret task")
}
