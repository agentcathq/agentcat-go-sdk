// Example: Minimal MCPCat integration with the official Go MCP SDK
//
// This shows the simplest possible MCPCat setup — just call Track() and defer
// shutdown. All tool calls, resource reads, and protocol events are captured
// automatically.
//
// Usage:
//
//	go run . (runs as an MCP server over HTTP Streamable on port 8083)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"

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
			Name:    "officialsdk-basic-example",
			Version: "1.0.0",
		},
		nil,
	)

	// --- MCPCat: 3 lines to add analytics ---
	projectID := os.Getenv("MCPCAT_PROJECT_ID")
	if projectID == "" {
		projectID = "proj_YOUR_PROJECT_ID"
	}
	shutdown, err := agentcat.Track(s, projectID, nil)
	if err != nil {
		log.Fatalf("agentcat: %v", err)
	}
	defer shutdown(context.Background())
	// --- end MCPCat ---

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
	log.Printf("MCP server listening on http://localhost:8083/mcp")
	if err := http.ListenAndServe(":8083", handler); err != nil {
		log.Fatalf("server: %v", err)
	}
}
