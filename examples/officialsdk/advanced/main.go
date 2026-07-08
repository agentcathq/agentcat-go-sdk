// Example: Full AgentCat integration with the official Go MCP SDK
//
// This demonstrates all AgentCat options: user identification, sensitive data
// redaction, debug logging, tool-call context capture, and missing-tool
// reporting.
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

	agentcat "go.agentcat.com/sdk/officialsdk"
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
		// Write debug logs to ~/mcpcat.log.
		Debug: true,

		// Identify the actor on first tool call.
		Identify: func(ctx context.Context, req *mcp.CallToolRequest) *agentcat.UserIdentity {
			// In a real server you would extract identity from ctx, headers,
			// or an auth token. Here we return a hard-coded example.
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
