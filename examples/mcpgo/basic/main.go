// Example: Minimal MCPCat integration with mark3labs/mcp-go
//
// This shows the simplest possible MCPCat setup — just call Track() and defer
// shutdown. All tool calls, resource reads, and protocol events are captured
// automatically.
//
// Usage:
//
//	go run . (runs as an MCP server over HTTP Streamable on port 8081)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/mcpgo"
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

func main() {
	s := server.NewMCPServer(
		"mcpgo-basic-example",
		"1.0.0",
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
	log.Printf("MCP server listening on http://localhost:8081/mcp")
	if err := httpServer.Start(":8081"); err != nil {
		log.Fatalf("server: %v", err)
	}
}
