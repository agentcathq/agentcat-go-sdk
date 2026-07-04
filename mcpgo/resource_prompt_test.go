package mcpgo

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// setupTrackedFullServer creates a tracked full-surface MCP server and an
// initialized in-process client ready for resource/prompt operations. It
// registers cleanup via t.Cleanup so callers do not need explicit defer calls.
func setupTrackedFullServer(t *testing.T) (client.MCPClient, context.Context) {
	t.Helper()

	mcpServer, _ := CreateFullServer()

	_, err := Track(mcpServer, "test_project", &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	})
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	t.Cleanup(func() { unregisterServer(mcpServer) })

	mcpClient, err := client.NewInProcessClient(mcpServer)
	if err != nil {
		t.Fatalf("Failed to create in-process client: %v", err)
	}
	t.Cleanup(func() { mcpClient.Close() })

	ctx := context.Background()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test-client", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Failed to initialize client: %v", err)
	}

	return mcpClient, ctx
}

// ---------------------------------------------------------------------------
// Resource tracking tests
// ---------------------------------------------------------------------------

// TestResourceTracking_ListResources verifies that ListResources returns the
// todo://about resource registered by CreateFullServer.
func TestResourceTracking_ListResources(t *testing.T) {
	mcpServer, _ := CreateFullServer()

	_, err := Track(mcpServer, "test_project", &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	})
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	defer unregisterServer(mcpServer)

	mcpClient, err := client.NewInProcessClient(mcpServer)
	if err != nil {
		t.Fatalf("Failed to create in-process client: %v", err)
	}
	defer mcpClient.Close()

	ctx := context.Background()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test-client", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Failed to initialize client: %v", err)
	}

	result, err := mcpClient.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}

	if len(result.Resources) == 0 {
		t.Fatal("Expected at least one resource, got none")
	}

	found := false
	for _, r := range result.Resources {
		if r.URI == "todo://about" {
			found = true
			if r.Name != "about" {
				t.Errorf("Expected resource name 'about', got '%s'", r.Name)
			}
			break
		}
	}
	if !found {
		t.Error("Expected resource 'todo://about' not found in ListResources result")
	}
}

// TestResourceTracking_ReadResource verifies that reading the todo://about
// resource returns the expected text content.
func TestResourceTracking_ReadResource(t *testing.T) {
	mcpServer, _ := CreateFullServer()

	_, err := Track(mcpServer, "test_project", &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	})
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	defer unregisterServer(mcpServer)

	mcpClient, err := client.NewInProcessClient(mcpServer)
	if err != nil {
		t.Fatalf("Failed to create in-process client: %v", err)
	}
	defer mcpClient.Close()

	ctx := context.Background()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test-client", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Failed to initialize client: %v", err)
	}

	readReq := mcp.ReadResourceRequest{}
	readReq.Params.URI = "todo://about"

	result, err := mcpClient.ReadResource(ctx, readReq)
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("Expected at least one resource content entry, got none")
	}

	// The handler returns mcp.TextResourceContents which satisfies the
	// mcp.ResourceContents interface.
	content := result.Contents[0]
	trc, ok := content.(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("Expected TextResourceContents, got %T", content)
	}

	if trc.URI != "todo://about" {
		t.Errorf("Expected URI 'todo://about', got '%s'", trc.URI)
	}
	if !strings.Contains(trc.Text, "todo server") {
		t.Errorf("Expected resource text to mention 'todo server', got: %s", trc.Text)
	}
}

// ---------------------------------------------------------------------------
// Prompt tracking tests
// ---------------------------------------------------------------------------

// TestPromptTracking_ListPrompts verifies that ListPrompts returns the
// summarize_todos prompt registered by CreateFullServer.
func TestPromptTracking_ListPrompts(t *testing.T) {
	mcpServer, _ := CreateFullServer()

	_, err := Track(mcpServer, "test_project", &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	})
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	defer unregisterServer(mcpServer)

	mcpClient, err := client.NewInProcessClient(mcpServer)
	if err != nil {
		t.Fatalf("Failed to create in-process client: %v", err)
	}
	defer mcpClient.Close()

	ctx := context.Background()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test-client", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Failed to initialize client: %v", err)
	}

	result, err := mcpClient.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		t.Fatalf("ListPrompts failed: %v", err)
	}

	if len(result.Prompts) == 0 {
		t.Fatal("Expected at least one prompt, got none")
	}

	found := false
	for _, p := range result.Prompts {
		if p.Name == "summarize_todos" {
			found = true
			if p.Description != "Summarize all current todos" {
				t.Errorf("Expected prompt description 'Summarize all current todos', got '%s'", p.Description)
			}
			break
		}
	}
	if !found {
		t.Error("Expected prompt 'summarize_todos' not found in ListPrompts result")
	}
}

// TestPromptTracking_GetPrompt verifies that GetPrompt with style="detailed"
// returns the expected description and messages.
func TestPromptTracking_GetPrompt(t *testing.T) {
	mcpServer, _ := CreateFullServer()

	_, err := Track(mcpServer, "test_project", &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	})
	if err != nil {
		t.Fatalf("Track failed: %v", err)
	}
	defer unregisterServer(mcpServer)

	mcpClient, err := client.NewInProcessClient(mcpServer)
	if err != nil {
		t.Fatalf("Failed to create in-process client: %v", err)
	}
	defer mcpClient.Close()

	ctx := context.Background()
	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("Failed to start client: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test-client", Version: "1.0.0"}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Failed to initialize client: %v", err)
	}

	getReq := mcp.GetPromptRequest{}
	getReq.Params.Name = "summarize_todos"
	getReq.Params.Arguments = map[string]string{"style": "detailed"}

	result, err := mcpClient.GetPrompt(ctx, getReq)
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}

	if result.Description != "Summary of all todos" {
		t.Errorf("Expected description 'Summary of all todos', got '%s'", result.Description)
	}

	if len(result.Messages) == 0 {
		t.Fatal("Expected at least one prompt message, got none")
	}

	// Verify the message contains the style parameter we passed.
	msg := result.Messages[0]
	if msg.Role != mcp.RoleUser {
		t.Errorf("Expected message role 'user', got '%s'", msg.Role)
	}

	// Extract text from the message content.
	tc, ok := msg.Content.(mcp.TextContent)
	if !ok {
		t.Fatalf("Expected TextContent in prompt message, got %T", msg.Content)
	}
	if !strings.Contains(tc.Text, "detailed") {
		t.Errorf("Expected prompt message to contain 'detailed' style, got: %s", tc.Text)
	}
	if !strings.Contains(tc.Text, "Summarize") {
		t.Errorf("Expected prompt message to contain 'Summarize', got: %s", tc.Text)
	}
}

// ---------------------------------------------------------------------------
// Combined surface test
// ---------------------------------------------------------------------------

// TestResourceAndPromptTracking_WithToolCalls exercises the full MCP surface
// area in a single session: listing tools, calling a tool, listing resources,
// reading a resource, listing prompts, and getting a prompt.
func TestResourceAndPromptTracking_WithToolCalls(t *testing.T) {
	mcpClient, ctx := setupTrackedFullServer(t)

	// 1. List tools
	toolsResult, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(toolsResult.Tools) == 0 {
		t.Fatal("Expected at least one tool, got none")
	}
	toolNames := make(map[string]bool, len(toolsResult.Tools))
	for _, tool := range toolsResult.Tools {
		toolNames[tool.Name] = true
	}
	if !toolNames["add_todo"] {
		t.Error("Expected tool 'add_todo' in ListTools result")
	}

	// 2. Call a tool (add a todo so the prompt has data to summarize)
	addReq := mcp.CallToolRequest{}
	addReq.Params.Name = "add_todo"
	addReq.Params.Arguments = map[string]any{
		"title":       "Integration test todo",
		"description": "Created during full-surface test",
	}
	addResult, err := mcpClient.CallTool(ctx, addReq)
	if err != nil {
		t.Fatalf("CallTool add_todo failed: %v", err)
	}
	if len(addResult.Content) == 0 {
		t.Fatal("Expected add_todo to return content")
	}
	if tc, ok := addResult.Content[0].(mcp.TextContent); ok {
		if !strings.Contains(tc.Text, "Added todo") {
			t.Errorf("Expected add_todo result to contain 'Added todo', got: %s", tc.Text)
		}
	}

	// 3. List resources
	resListResult, err := mcpClient.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}
	foundResource := false
	for _, r := range resListResult.Resources {
		if r.URI == "todo://about" {
			foundResource = true
			break
		}
	}
	if !foundResource {
		t.Error("Expected resource 'todo://about' in ListResources result")
	}

	// 4. Read resource
	readReq := mcp.ReadResourceRequest{}
	readReq.Params.URI = "todo://about"
	readResult, err := mcpClient.ReadResource(ctx, readReq)
	if err != nil {
		t.Fatalf("ReadResource failed: %v", err)
	}
	if len(readResult.Contents) == 0 {
		t.Fatal("Expected resource content, got none")
	}

	// 5. List prompts
	promptListResult, err := mcpClient.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		t.Fatalf("ListPrompts failed: %v", err)
	}
	foundPrompt := false
	for _, p := range promptListResult.Prompts {
		if p.Name == "summarize_todos" {
			foundPrompt = true
			break
		}
	}
	if !foundPrompt {
		t.Error("Expected prompt 'summarize_todos' in ListPrompts result")
	}

	// 6. Get prompt -- should now include the todo we added
	getReq := mcp.GetPromptRequest{}
	getReq.Params.Name = "summarize_todos"
	getReq.Params.Arguments = map[string]string{"style": "detailed"}
	promptResult, err := mcpClient.GetPrompt(ctx, getReq)
	if err != nil {
		t.Fatalf("GetPrompt failed: %v", err)
	}
	if promptResult.Description != "Summary of all todos" {
		t.Errorf("Expected description 'Summary of all todos', got '%s'", promptResult.Description)
	}
	if len(promptResult.Messages) == 0 {
		t.Fatal("Expected at least one prompt message")
	}

	// The prompt message should reference both the style and the todo we added.
	tc, ok := promptResult.Messages[0].Content.(mcp.TextContent)
	if !ok {
		t.Fatalf("Expected TextContent in prompt message, got %T", promptResult.Messages[0].Content)
	}
	if !strings.Contains(tc.Text, "detailed") {
		t.Errorf("Expected prompt to reference 'detailed' style, got: %s", tc.Text)
	}
	if !strings.Contains(tc.Text, "Integration test todo") {
		t.Errorf("Expected prompt to include added todo title, got: %s", tc.Text)
	}
}
