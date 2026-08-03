package mcpgo

import (
	"net/http"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestExtractExtra_CallToolRequest(t *testing.T) {
	req := &mcp.CallToolRequest{}
	req.Header = http.Header{
		"Authorization": []string{"Bearer tok_123"},
		"User-Agent":    []string{"claude-desktop/1.0"},
	}

	extra := extractExtra(req)
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

func TestExtractExtra_ReadResourceRequest(t *testing.T) {
	req := &mcp.ReadResourceRequest{}
	req.Header = http.Header{
		"X-Custom": []string{"value"},
	}

	extra := extractExtra(req)
	if extra == nil {
		t.Fatal("expected non-nil extra")
	}

	headers, ok := extra["header"].(http.Header)
	if !ok {
		t.Fatal("expected header to be http.Header")
	}
	if headers.Get("X-Custom") != "value" {
		t.Errorf("expected X-Custom header, got %v", headers.Get("X-Custom"))
	}
}

func TestExtractExtra_NilMessage(t *testing.T) {
	extra := extractExtra(nil)
	if extra != nil {
		t.Errorf("expected nil extra for nil message, got %v", extra)
	}
}

func TestExtractExtra_NoHeaders(t *testing.T) {
	req := &mcp.CallToolRequest{}

	extra := extractExtra(req)
	if extra != nil {
		t.Errorf("expected nil extra when no headers, got %v", extra)
	}
}

func TestExtractExtra_EmptyHeaders(t *testing.T) {
	req := &mcp.CallToolRequest{}
	req.Header = http.Header{}

	extra := extractExtra(req)
	if extra != nil {
		t.Errorf("expected nil extra for empty headers, got %v", extra)
	}
}

func TestExtractExtra_GetPromptRequest(t *testing.T) {
	req := &mcp.GetPromptRequest{}
	req.Header = http.Header{
		"Accept": []string{"application/json"},
	}

	extra := extractExtra(req)
	if extra == nil {
		t.Fatal("expected non-nil extra")
	}

	headers, ok := extra["header"].(http.Header)
	if !ok {
		t.Fatal("expected header to be http.Header")
	}
	if headers.Get("Accept") != "application/json" {
		t.Errorf("expected Accept header, got %v", headers.Get("Accept"))
	}
}

func TestExtractExtra_NonStructMessage(t *testing.T) {
	extra := extractExtra("not a struct")
	if extra != nil {
		t.Errorf("expected nil extra for non-struct message, got %v", extra)
	}
}

func TestExtractResponse_CallToolResult(t *testing.T) {
	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: "hello world"},
		},
		StructuredContent: map[string]any{"greeting": "hello"},
		IsError:           false,
	}

	resp := extractResponse(result)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp["isError"] != false {
		t.Error("expected isError to be false")
	}
	if resp["content"] == nil {
		t.Error("expected content to be present")
	}
	if resp["structuredContent"] == nil {
		t.Error("expected structuredContent to be present")
	}
}

func TestExtractResponse_NilResponse(t *testing.T) {
	if resp := extractResponse(nil); resp != nil {
		t.Errorf("expected nil response for nil input, got %v", resp)
	}
}
