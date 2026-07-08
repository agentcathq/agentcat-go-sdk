package mcpgo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
)

// otlpSpy records raw OTLP trace request bodies.
type otlpSpy struct {
	mu     sync.Mutex
	bodies []string
}

func (s *otlpSpy) add(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, body)
}

func (s *otlpSpy) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

// waitFor polls until at least one recorded body satisfies the predicate.
func (s *otlpSpy) waitFor(t *testing.T, timeout time.Duration, predicate func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, b := range s.all() {
			if predicate(b) {
				return b
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no OTLP payload matched within %v (received %d payloads)", timeout, len(s.all()))
	return ""
}

// TestTrack_TelemetryOnlyMode exercises the full pipeline with an empty
// project ID and a configured OTLP exporter: events must reach the exporter
// while the AgentCat API send is skipped.
func TestTrack_TelemetryOnlyMode(t *testing.T) {
	spy := &otlpSpy{}
	otlpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		spy.add(string(b))
		w.WriteHeader(http.StatusOK)
	}))
	defer otlpSrv.Close()

	// A uniquely named server so this test's spans are distinguishable from
	// events of other tests that drain through the shared global publisher.
	mcpServer := server.NewMCPServer("telemetry-only-server", "1.0.0",
		server.WithToolCapabilities(true))
	mcpServer.AddTool(
		mcp.NewTool("echo_telemetry", mcp.WithString("title", mcp.Required())),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	)

	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.Exporters = map[string]ExporterConfig{
		"otlp": {Type: "otlp", Endpoint: otlpSrv.URL},
	}

	// Empty projectID with at least one exporter must be accepted.
	shutdown, err := Track(mcpServer, "", opts)
	if err != nil {
		t.Fatalf("Track with exporters and empty projectID failed: %v", err)
	}
	t.Cleanup(func() {
		unregisterServer(mcpServer)
	})

	// Use a real HTTP transport: it exercises the full session-capture code
	// path (in-process clients do not carry a client session in hook contexts).
	httpServer := server.NewTestStreamableHTTPServer(mcpServer)
	defer httpServer.Close()

	mcpClient, err := client.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		t.Fatalf("NewStreamableHttpClient: %v", err)
	}
	defer mcpClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{Name: "telemetry-client", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(ctx, initRequest); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = "echo_telemetry"
	req.Params.Arguments = map[string]any{"title": "telemetry only"}
	if _, err := mcpClient.CallTool(ctx, req); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	// The tool call event must reach the OTLP exporter even though no
	// project ID is configured. Match this test's span specifically: events
	// from other tests can also drain through the shared global publisher.
	body := spy.waitFor(t, 5*time.Second, func(b string) bool {
		return strings.Contains(b, "mcp:tools/call") && strings.Contains(b, `"echo_telemetry"`)
	})

	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("OTLP payload is not JSON: %v", err)
	}
	// No project ID attribute should be present in telemetry-only mode
	// (empty attributes are filtered).
	if strings.Contains(body, "mcp.project_id") {
		t.Errorf("OTLP payload should not contain mcp.project_id in telemetry-only mode")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelShutdown()
	if err := shutdown(shutdownCtx); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestTrack_EmptyProjectIDWithoutExportersStillFails(t *testing.T) {
	mcpServer, _ := CreateFullServer()

	_, err := Track(mcpServer, "", nil)
	if !errors.Is(err, agentcat.ErrEmptyProjectID) {
		t.Fatalf("Track error = %v, want ErrEmptyProjectID", err)
	}
}
