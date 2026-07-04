package officialsdk

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestExtractParameters_ToolsCall(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"query":   "hello",
		"context": "Testing user intent",
	})
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "test_tool",
			Arguments: json.RawMessage(args),
		},
	}

	params := extractParameters("tools/call", req)
	if params == nil {
		t.Fatal("expected non-nil params")
	}
	if params["name"] != "test_tool" {
		t.Errorf("expected name 'test_tool', got %v", params["name"])
	}
	// "context" should be filtered out from arguments
	if argsMap, ok := params["arguments"].(map[string]any); ok {
		if _, hasContext := argsMap["context"]; hasContext {
			t.Error("context param should be filtered from arguments")
		}
		if argsMap["query"] != "hello" {
			t.Errorf("expected query 'hello', got %v", argsMap["query"])
		}
	} else {
		t.Error("expected arguments map")
	}
}

func TestExtractParameters_ToolsCall_NoArguments(t *testing.T) {
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name: "tool_no_args",
		},
	}

	params := extractParameters("tools/call", req)
	if params == nil {
		t.Fatal("expected non-nil params")
	}
	if params["name"] != "tool_no_args" {
		t.Errorf("expected name 'tool_no_args', got %v", params["name"])
	}
	if _, ok := params["arguments"]; ok {
		t.Error("expected no arguments key when args are empty")
	}
}

func TestExtractParameters_ToolsCall_OnlyContextArgument(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"context": "just context, nothing else",
	})
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      "tool_context_only",
			Arguments: json.RawMessage(args),
		},
	}

	params := extractParameters("tools/call", req)
	if params == nil {
		t.Fatal("expected non-nil params")
	}
	// Only "context" arg, which gets filtered out, so no "arguments" key
	if _, ok := params["arguments"]; ok {
		t.Error("expected no arguments key when only context is present")
	}
}

func TestExtractParameters_ResourcesRead(t *testing.T) {
	req := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: "file:///test.txt",
		},
	}

	params := extractParameters("resources/read", req)
	if params == nil {
		t.Fatal("expected non-nil params")
	}
	if params["uri"] != "file:///test.txt" {
		t.Errorf("expected uri 'file:///test.txt', got %v", params["uri"])
	}
}

func TestExtractParameters_PromptsGet(t *testing.T) {
	req := &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{
			Name:      "test_prompt",
			Arguments: map[string]string{"key": "value"},
		},
	}

	params := extractParameters("prompts/get", req)
	if params == nil {
		t.Fatal("expected non-nil params")
	}
	if params["name"] != "test_prompt" {
		t.Errorf("expected name 'test_prompt', got %v", params["name"])
	}
	if args, ok := params["arguments"].(map[string]string); ok {
		if args["key"] != "value" {
			t.Errorf("expected argument key=value, got %v", args["key"])
		}
	} else {
		t.Error("expected arguments map[string]string")
	}
}

func TestExtractParameters_Initialize(t *testing.T) {
	// On the server side, initialize comes as ServerRequest[*InitializeParams]
	req := &mcp.ServerRequest[*mcp.InitializeParams]{
		Params: &mcp.InitializeParams{
			ProtocolVersion: "2024-11-05",
			ClientInfo: &mcp.Implementation{
				Name:    "test-client",
				Version: "1.0.0",
			},
		},
	}

	params := extractParameters("initialize", req)
	if params == nil {
		t.Fatal("expected non-nil params")
	}
	if params["protocolVersion"] != "2024-11-05" {
		t.Errorf("expected protocolVersion '2024-11-05', got %v", params["protocolVersion"])
	}
	if params["clientInfo"] == nil {
		t.Error("expected non-nil clientInfo")
	}
}

func TestExtractParameters_UnknownMethod(t *testing.T) {
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name: "test",
		},
	}

	params := extractParameters("unknown/method", req)
	if params != nil {
		t.Errorf("expected nil params for unknown method, got %v", params)
	}
}

func TestExtractParameters_NilRequest(t *testing.T) {
	params := extractParameters("tools/call", nil)
	if params != nil {
		t.Errorf("expected nil params for nil request, got %v", params)
	}
}

func TestExtractResponse_ToolsCall(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "hello world"},
		},
		IsError: false,
	}

	resp := extractResponse("tools/call", result)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp["isError"] != false {
		t.Error("expected isError to be false")
	}
	if resp["content"] == nil {
		t.Error("expected content to be present")
	}
}

func TestExtractResponse_ToolsCall_Error(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "something went wrong"},
		},
		IsError: true,
	}

	resp := extractResponse("tools/call", result)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp["isError"] != true {
		t.Error("expected isError to be true")
	}
}

func TestExtractResponse_ResourcesRead(t *testing.T) {
	result := &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{URI: "file:///test.txt"},
		},
	}

	resp := extractResponse("resources/read", result)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp["contents"] == nil {
		t.Error("expected contents to be present")
	}
}

func TestExtractResponse_PromptsGet(t *testing.T) {
	result := &mcp.GetPromptResult{
		Description: "A test prompt",
		Messages: []*mcp.PromptMessage{
			{
				Role:    "user",
				Content: &mcp.TextContent{Text: "hello"},
			},
		},
	}

	resp := extractResponse("prompts/get", result)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp["description"] != "A test prompt" {
		t.Errorf("expected description 'A test prompt', got %v", resp["description"])
	}
	if resp["messages"] == nil {
		t.Error("expected messages to be present")
	}
}

func TestExtractResponse_ToolsList(t *testing.T) {
	result := &mcp.ListToolsResult{
		Tools: []*mcp.Tool{
			{
				Name:        "tool1",
				Description: "A test tool",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	resp := extractResponse("tools/list", result)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp["tools"] == nil {
		t.Error("expected tools to be present")
	}
}

func TestExtractResponse_Initialize(t *testing.T) {
	result := &mcp.InitializeResult{
		ProtocolVersion: "2024-11-05",
		ServerInfo: &mcp.Implementation{
			Name:    "test-server",
			Version: "1.0.0",
		},
	}

	resp := extractResponse("initialize", result)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp["protocolVersion"] != "2024-11-05" {
		t.Errorf("expected protocolVersion '2024-11-05', got %v", resp["protocolVersion"])
	}
	if resp["serverInfo"] == nil {
		t.Error("expected serverInfo to be present")
	}
}

func TestExtractResponse_NilResult(t *testing.T) {
	resp := extractResponse("tools/call", nil)
	if resp != nil {
		t.Errorf("expected nil response for nil input, got %v", resp)
	}
}

func TestExtractResponse_UnknownMethod(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "hello"},
		},
	}

	resp := extractResponse("unknown/method", result)
	if resp != nil {
		t.Errorf("expected nil response for unknown method, got %v", resp)
	}
}

func TestExtractToolName(t *testing.T) {
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name: "my_tool",
		},
	}

	name := extractToolName(req)
	if name != "my_tool" {
		t.Errorf("expected 'my_tool', got '%s'", name)
	}
}

func TestExtractToolName_NonToolRequest(t *testing.T) {
	req := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: "file:///test.txt",
		},
	}

	name := extractToolName(req)
	if name != "" {
		t.Errorf("expected empty string for non-tool request, got '%s'", name)
	}
}

func TestExtractResourceURI(t *testing.T) {
	req := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: "file:///test.txt",
		},
	}

	uri := extractResourceURI(req)
	if uri != "file:///test.txt" {
		t.Errorf("expected 'file:///test.txt', got '%s'", uri)
	}
}

func TestExtractResourceURI_NonResourceRequest(t *testing.T) {
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name: "tool",
		},
	}

	uri := extractResourceURI(req)
	if uri != "" {
		t.Errorf("expected empty string for non-resource request, got '%s'", uri)
	}
}

func TestUnmarshalArguments_ValidJSON(t *testing.T) {
	raw := json.RawMessage(`{"key": "value", "num": 42}`)
	args := unmarshalArguments(raw)
	if args == nil {
		t.Fatal("expected non-nil args")
	}
	if args["key"] != "value" {
		t.Errorf("expected key=value, got %v", args["key"])
	}
}

func TestUnmarshalArguments_Empty(t *testing.T) {
	args := unmarshalArguments(nil)
	if args != nil {
		t.Errorf("expected nil for nil input, got %v", args)
	}

	args = unmarshalArguments(json.RawMessage{})
	if args != nil {
		t.Errorf("expected nil for empty input, got %v", args)
	}
}

func TestUnmarshalArguments_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`{invalid json`)
	args := unmarshalArguments(raw)
	if args != nil {
		t.Errorf("expected nil for invalid JSON, got %v", args)
	}
}

func TestExtractExtra_WithHeaders(t *testing.T) {
	serverReq := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Extra: &mcp.RequestExtra{
			Header: http.Header{
				"Authorization": []string{"Bearer tok_123"},
				"User-Agent":    []string{"claude-desktop/1.0"},
			},
		},
	}

	extra := extractExtra(serverReq)
	if extra == nil {
		t.Fatal("expected non-nil extra")
	}

	headers, ok := extra["header"].(http.Header)
	if !ok {
		t.Fatal("expected header to be http.Header")
	}
	if headers.Get("Authorization") != "Bearer tok_123" {
		t.Errorf("expected Authorization header, got %v", headers.Get("Authorization"))
	}
}

func TestExtractExtra_HeadersOnly(t *testing.T) {
	serverReq := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Extra: &mcp.RequestExtra{
			Header: http.Header{
				"User-Agent": []string{"test-agent"},
			},
		},
	}

	extra := extractExtra(serverReq)
	if extra == nil {
		t.Fatal("expected non-nil extra")
	}
	if _, ok := extra["header"]; !ok {
		t.Error("expected header key")
	}
	if _, ok := extra["tokenInfo"]; ok {
		t.Error("expected no tokenInfo key when TokenInfo is nil")
	}
}

func TestExtractExtra_NilExtra(t *testing.T) {
	serverReq := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Extra: nil,
	}

	extra := extractExtra(serverReq)
	if extra != nil {
		t.Errorf("expected nil extra, got %v", extra)
	}
}

func TestExtractExtra_ClientRequest(t *testing.T) {
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: "test"},
	}

	extra := extractExtra(req)
	if extra != nil {
		t.Errorf("expected nil extra for ClientRequest, got %v", extra)
	}
}

func TestExtractExtra_NilRequest(t *testing.T) {
	extra := extractExtra(nil)
	if extra != nil {
		t.Errorf("expected nil extra for nil request, got %v", extra)
	}
}

func TestExtractExtra_EmptyHeaders(t *testing.T) {
	serverReq := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Extra: &mcp.RequestExtra{
			Header: http.Header{},
		},
	}

	extra := extractExtra(serverReq)
	if extra != nil {
		t.Errorf("expected nil extra when headers are empty and no tokenInfo, got %v", extra)
	}
}
