// Example: Full AgentCat integration with the official Go MCP SDK
//
// This demonstrates the AgentCat v2 options: per-call user identification,
// agent tracking, sensitive data redaction, debug logging, tool-call context
// capture, and missing-tool reporting. It also shows the hook-mode variant, in
// which you supply your own correlation ID instead of letting the SDK inject a
// session_id parameter.
//
// Usage:
//
//	go run . (runs as an MCP server over HTTP Streamable on port 8084)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/officialsdk/v2"
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

type EchoArgs struct {
	Text string `json:"text" jsonschema:"the text to echo"`
}

type ReverseArgs struct {
	Text string `json:"text" jsonschema:"the text to reverse"`
}

type CountCharsArgs struct {
	Text string `json:"text" jsonschema:"the text to count characters in"`
}

type TextResult struct {
	Text string `json:"text"`
}

func main() {
	s := mcp.NewServer(
		&mcp.Implementation{
			Name:    "officialsdk-advanced-example",
			Version: "1.0.0",
		},
		nil,
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
		Identify: func(ctx context.Context, request mcp.Request) *agentcat.UserIdentity {
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
		//	ResolveSessionID: func(ctx context.Context, req mcp.Request) (string, error) {
		//		return workflowIDFromContext(ctx), nil
		//	},
	})
	if err != nil {
		log.Fatalf("agentcat: %v", err)
	}
	defer shutdown(context.Background())
	// --- end AgentCat ---

	mcp.AddTool(s, &mcp.Tool{
		Name:        "echo",
		Description: "Echo back the input text",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args EchoArgs) (*mcp.CallToolResult, TextResult, error) {
		return nil, TextResult{Text: args.Text}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "reverse",
		Description: "Reverse the input text",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ReverseArgs) (*mcp.CallToolResult, TextResult, error) {
		runes := []rune(args.Text)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return nil, TextResult{Text: string(runes)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "count_chars",
		Description: "Count the number of characters in the input text",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args CountCharsArgs) (*mcp.CallToolResult, TextResult, error) {
		return nil, TextResult{Text: fmt.Sprintf("%d", len([]rune(args.Text)))}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "error_test",
		Description: "Always errors — use this to test stack trace capture",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args EchoArgs) (*mcp.CallToolResult, TextResult, error) {
		return nil, TextResult{}, dangerousOperation(args.Text)
	})

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return s
	}, nil)
	log.Printf("MCP server listening on http://localhost:8084/mcp")
	if err := http.ListenAndServe(":8084", handler); err != nil {
		log.Fatalf("server: %v", err)
	}
}
