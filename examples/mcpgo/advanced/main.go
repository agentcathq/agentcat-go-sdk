// Example: Full AgentCat integration with mark3labs/mcp-go
//
// This demonstrates the AgentCat v2 options: per-call user identification,
// agent tracking, sensitive data redaction, debug logging, tool-call context
// capture, and missing-tool reporting. It also shows the hook-mode variant, in
// which you supply your own correlation ID instead of letting the SDK inject a
// session_id parameter, and how AgentCat composes with hooks you register
// yourself.
//
// Usage:
//
//	go run . (runs as an MCP server over HTTP Streamable on port 8082)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/mcpgo/v2"
)

func processData(input string) error {
	if input == "" {
		return errors.New("input must not be empty")
	}
	return fmt.Errorf("data processing failed for %q: %w", input, errors.New("invalid payload structure"))
}

func validateInput(input string) error {
	if err := processData(input); err != nil {
		return fmt.Errorf("validation error: %w", err)
	}
	return nil
}

func dangerousOperation(input string) error {
	if err := validateInput(input); err != nil {
		return fmt.Errorf("dangerous operation aborted: %w", err)
	}
	return nil
}

// emailRegex matches common email patterns for redaction.
var emailRegex = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

func main() {
	// Hooks you register yourself keep working: AgentCat reads the server's
	// hooks back with MCPServer.GetHooks() and appends its own to them, so
	// register yours at construction time as usual. (v1's Options.Hooks field
	// is gone — it existed only to hand AgentCat hooks it can now find itself.)
	myHooks := &server.Hooks{}
	myHooks.AddBeforeCallTool(func(ctx context.Context, id any, message *mcp.CallToolRequest) {
		log.Printf("about to call %s", message.Params.Name)
	})

	s := server.NewMCPServer(
		"mcpgo-advanced-example",
		"1.0.0",
		server.WithHooks(myHooks),
	)

	// --- AgentCat: full options ---
	projectID := os.Getenv("MCPCAT_PROJECT_ID")
	if projectID == "" {
		projectID = "proj_YOUR_PROJECT_ID"
	}
	shutdown, err := agentcat.Track(s, projectID, &agentcat.Options{
		// Write debug logs to ~/agentcat.log.
		Debug: true,

		// Also inject a required "agent_id" parameter, so parallel agents
		// working the same session can be told apart. Off by default. An omitted
		// agent_id never rejects the call.
		EnableAgentTracking: true,

		// Identify the actor. In v2 this runs on every tool call, uncached,
		// and stamps only that call's event — so keep it cheap and do NOT make
		// network calls here. request is always the *mcp.CallToolRequest that
		// triggered the event.
		Identify: func(ctx context.Context, request any) *agentcat.UserIdentity {
			// In a real server you would extract identity from ctx, headers,
			// or an already-parsed auth token. Here we return a hard-coded
			// example.
			return &agentcat.UserIdentity{
				UserID:   "user-123",
				UserName: "John Doe",
				UserData: map[string]any{
					"plan": "pro",
				},
			}
		},

		// Strip email addresses from all captured data before it leaves the process.
		RedactSensitiveInformation: func(text string) string {
			return emailRegex.ReplaceAllString(text, "[REDACTED_EMAIL]")
		},

		// Both injected extras are on by default; uncomment to opt out.
		//
		// The "context" parameter asks the agent to explain why it is calling
		// the tool, which is what powers user-intent analytics. Opting out
		// keeps handle correlation but loses intent:
		//
		//	DisableToolCallContext: true,
		//
		// get_more_tools is an extra tool AgentCat registers so an agent can
		// report capabilities you do not offer yet:
		//
		//	DisableReportMissing: true,

		// Hook mode — an alternative to the injected session_id parameter.
		// Setting ResolveSessionID stops AgentCat from injecting session_id into any
		// tool and stops it from ever showing session instructions to the agent:
		// you own correlation. Return your own ID (a workflow/trace/thread ID)
		// and AgentCat derives the same ses_ session from it deterministically,
		// across processes, restarts, and languages. Return "" or an error to
		// silently mint a random session for that one call. Like Identify, it runs
		// on every tool call — keep it cheap.
		//
		//	ResolveSessionID: func(ctx context.Context, request mcp.CallToolRequest) (string, error) {
		//		return workflowIDFromContext(ctx), nil
		//	},
	})
	if err != nil {
		log.Fatalf("agentcat: %v", err)
	}
	defer shutdown(context.Background())
	// --- end AgentCat ---

	s.AddTool(
		mcp.NewTool("echo",
			mcp.WithDescription("Echo back the input text"),
			mcp.WithString("text", mcp.Required(), mcp.Description("Text to echo")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			text, _ := req.RequireString("text")
			return mcp.NewToolResultText(text), nil
		},
	)

	s.AddTool(
		mcp.NewTool("reverse",
			mcp.WithDescription("Reverse the input text"),
			mcp.WithString("text", mcp.Required(), mcp.Description("Text to reverse")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			text, _ := req.RequireString("text")
			runes := []rune(text)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			return mcp.NewToolResultText(string(runes)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("count_chars",
			mcp.WithDescription("Count the number of characters in the input text"),
			mcp.WithString("text", mcp.Required(), mcp.Description("Text to count")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			text, _ := req.RequireString("text")
			return mcp.NewToolResultText(fmt.Sprintf("%d", len([]rune(text)))), nil
		},
	)

	s.AddTool(
		mcp.NewTool("error_test",
			mcp.WithDescription("Always errors — use this to test stack trace capture"),
			mcp.WithString("text", mcp.Required(), mcp.Description("Text to include in the error")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			text, _ := req.RequireString("text")
			return nil, dangerousOperation(text)
		},
	)

	httpServer := server.NewStreamableHTTPServer(s)
	log.Printf("MCP server listening on http://localhost:8082/mcp")
	if err := httpServer.Start(":8082"); err != nil {
		log.Fatalf("server: %v", err)
	}
}
