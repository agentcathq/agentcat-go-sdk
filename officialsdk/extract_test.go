package officialsdk

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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

func TestExtractResponse_ToolsCall_StructuredContent(t *testing.T) {
	result := &mcp.CallToolResult{
		StructuredContent: json.RawMessage(`{"text":"hi"}`),
	}

	resp := extractResponse("tools/call", result)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	sc, ok := resp["structuredContent"].(map[string]any)
	if !ok || sc["text"] != "hi" {
		t.Errorf("structuredContent not converted: %v", resp["structuredContent"])
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
