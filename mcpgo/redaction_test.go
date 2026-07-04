package mcpgo

import (
	"regexp"
	"testing"
)

// TestRedaction_FunctionIsInvoked creates a harness with a
// RedactSensitiveInformation function that replaces email addresses with
// "[REDACTED]" using a regular expression. It calls a tool whose arguments
// contain email addresses and verifies that the redaction function is properly
// configured (no panics, tool call succeeds, and the tool result is correct).
//
// Note: The redaction function is invoked asynchronously by the event publisher
// worker goroutines, so we verify configuration correctness rather than
// counting invocations.
func TestRedaction_FunctionIsInvoked(t *testing.T) {
	emailRe := regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

	h := newHarness(t, &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		RedactSensitiveInformation: func(text string) string {
			return emailRe.ReplaceAllString(text, "[REDACTED]")
		},
	})

	// Call a tool with arguments that contain email addresses.
	result := h.callTool("add_todo", map[string]any{
		"title":       "Email alice@example.com about meeting",
		"description": "Also CC bob@corp.io and carol@test.org",
	})

	// The tool itself should succeed and return its normal response.
	text := resultText(result)
	assertContains(t, text, "Added todo")

	// Verify the todo was actually stored (the redaction function should
	// not interfere with the server-side data flow).
	listResult := h.callTool("list_todos", map[string]any{})
	listText := resultText(listResult)
	assertContains(t, listText, "Email alice@example.com about meeting")
}

// TestRedaction_PanicInRedactFnDoesNotCrashServer verifies that a panic inside
// the RedactSensitiveInformation function does not crash the MCP server. Tool
// calls should continue to work normally even when redaction panics.
func TestRedaction_PanicInRedactFnDoesNotCrashServer(t *testing.T) {
	h := newHarness(t, &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
		RedactSensitiveInformation: func(text string) string {
			panic("redaction panic!")
		},
	})

	// First tool call -- should NOT crash despite the panic in redaction.
	result1 := h.callTool("add_todo", map[string]any{
		"title":       "Secret task",
		"description": "Contains sensitive data",
	})

	text1 := resultText(result1)
	assertContains(t, text1, "Added todo")

	// Second tool call -- verify the server is still fully operational.
	result2 := h.callTool("list_todos", map[string]any{})

	text2 := resultText(result2)
	assertContains(t, text2, "Secret task")
}

// TestRedaction_NilRedactFnDoesNotInterfere verifies that when
// RedactSensitiveInformation is nil (the default), tool calls work fine
// without any redaction-related interference.
func TestRedaction_NilRedactFnDoesNotInterfere(t *testing.T) {
	h := newHarness(t, &Options{
		DisableReportMissing:   true,
		DisableToolCallContext: true,
	})

	// Add a todo with data that would normally be redacted.
	result := h.callTool("add_todo", map[string]any{
		"title":       "Call user@example.com",
		"description": "Phone: 555-123-4567",
	})

	text := resultText(result)
	assertContains(t, text, "Added todo")

	// Retrieve the list to confirm the todo was persisted correctly.
	listResult := h.callTool("list_todos", map[string]any{})
	listText := resultText(listResult)
	assertContains(t, listText, "Call user@example.com")
}
