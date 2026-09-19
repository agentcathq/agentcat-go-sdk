package mcpgo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk/v2"
)

// Vectors: {"text":"hi there"} is 19 bytes -> 6 tokens; "hello world" is 11
// bytes -> 4; "boom!" is 5 bytes -> 2.
func TestToolCallEventsCarryTokenEstimates(t *testing.T) {
	mcpServer := server.NewMCPServer("tokens", "1.0.0", server.WithToolCapabilities(true))
	mcpServer.AddTool(
		mcp.NewTool("token_probe", mcp.WithDescription("fixed reply"), mcp.WithString("text")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			res := mcp.NewToolResultText("hello world")
			res.StructuredContent = map[string]any{"padding": "yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"}
			return res, nil
		},
	)
	mcpServer.AddTool(
		mcp.NewTool("error_result", mcp.WithDescription("isError result"), mcp.WithString("text")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultError("boom!"), nil
		},
	)

	opts := DefaultOptions()
	opts.DisableToolCallContext = true
	opts.DisableReportMissing = true
	instance := newTestInstance(mcpServer, "test_project", opts)
	agentcat.RegisterServer(mcpServer, instance)
	t.Cleanup(func() { unregisterServer(mcpServer) })
	mock := &mockPublisher{}
	installTracking(mcpServer, instance, opts, mock.publish)

	callToolRaw(t, mcpServer, "token_probe", map[string]any{"text": "hi there"})
	callToolRaw(t, mcpServer, "error_result", map[string]any{"text": "hi there"})

	events := filterEvents(mock.getEvents(), "mcp:tools/call")
	if len(events) != 2 {
		t.Fatalf("expected two tool-call events, got %d", len(events))
	}

	probe := events[0]
	if probe.InputTokens == nil || *probe.InputTokens != 6 {
		t.Errorf("token_probe InputTokens = %v, want 6", probe.InputTokens)
	}
	if probe.OutputTokens == nil || *probe.OutputTokens != 4 {
		t.Errorf("token_probe OutputTokens = %v, want 4", probe.OutputTokens)
	}

	// mcp-go records no Response on an isError result, so only the input
	// side is estimated; the server falls back for the output side.
	errored := events[1]
	if errored.InputTokens == nil || *errored.InputTokens != 6 {
		t.Errorf("error_result InputTokens = %v, want 6", errored.InputTokens)
	}
	if errored.OutputTokens != nil {
		t.Errorf("error_result OutputTokens = %v, want nil", *errored.OutputTokens)
	}
}

// Ordering: the counts describe the raw bytes even though the publisher's
// redaction hook rewrites every string before the event reaches the wire.
// {"title":"secret-value-123456789"} is 34 bytes -> 10 tokens. add_todo's
// reply embeds a fresh ID, so only the input side is pinned to a value.
func TestTokenEstimatesSurviveRedaction(t *testing.T) {
	resetPublisher(t)
	api := newCaptureAPI(t)

	mcpClient := setupStreamableHTTP(t, &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		APIBaseURL:             api.srv.URL,
		// Only the secret string is rewritten: a hook that replaced every
		// string would also clobber the content block's "type" discriminator.
		RedactSensitiveInformation: func(text string) string {
			if strings.Contains(text, "secret") {
				return "[REDACTED]"
			}
			return text
		},
	})

	callToolHTTP(t, mcpClient, "add_todo", map[string]any{"title": "secret-value-123456789"})

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
		if !strings.Contains(b, "mcp:tools/call") {
			continue
		}
		var body struct {
			InputTokens  *int32         `json:"input_tokens"`
			OutputTokens *int32         `json:"output_tokens"`
			Parameters   map[string]any `json:"parameters"`
		}
		if err := json.Unmarshal([]byte(b), &body); err != nil {
			t.Fatalf("decode published body: %v", err)
		}
		if strings.Contains(b, "secret-value-123456789") {
			t.Errorf("raw text leaked past the redaction hook: %s", b)
		}
		args, _ := body.Parameters["arguments"].(map[string]any)
		if args["title"] != "[REDACTED]" {
			t.Errorf("parameters.arguments.title = %v, want [REDACTED]", args["title"])
		}
		if body.InputTokens == nil || *body.InputTokens != 10 {
			t.Errorf("input_tokens = %v, want 10 (the raw bytes)", body.InputTokens)
		}
		if body.OutputTokens == nil || *body.OutputTokens < 1 {
			t.Errorf("output_tokens = %v, want a positive count", body.OutputTokens)
		}
	}
}
