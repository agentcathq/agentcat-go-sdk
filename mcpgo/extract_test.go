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
