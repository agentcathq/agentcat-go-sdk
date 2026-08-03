package mcpgo

import (
	"net/http"
	"reflect"

	"github.com/mark3labs/mcp-go/mcp"
	agentcat "go.agentcat.com/sdk/v2"
)

// extractResponse renders a tool call's result for the published event. Only
// tools/call results are recorded: v2 publishes no other event type.
func extractResponse(result *mcp.CallToolResult) map[string]any {
	if result == nil {
		return nil
	}

	resp := make(map[string]any)
	if result.StructuredContent != nil {
		resp["structuredContent"] = agentcat.ConvertToMap(result.StructuredContent)
	}
	if len(result.Content) > 0 {
		resp["content"] = agentcat.ConvertToMap(result.Content)
	}
	resp["isError"] = result.IsError

	return resp
}

// extractExtra extracts transport-layer metadata from the request message.
// For HTTP transports, mcp-go request types have a Header field populated
// with the incoming HTTP headers. Uses reflection to access the Header field
// from any request type without maintaining a type switch.
// Returns nil if no extra data is available (e.g., stdio transport).
func extractExtra(message any) map[string]any {
	if message == nil {
		return nil
	}

	v := reflect.ValueOf(message)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}

	// Guard the reflection carefully: IsNil panics on non-nilable kinds and
	// Interface panics on fields obtained through unexported embedding, so
	// check Kind and CanInterface before either call.
	headerField := v.FieldByName("Header")
	if !headerField.IsValid() || headerField.Kind() != reflect.Map ||
		!headerField.CanInterface() || headerField.IsNil() {
		return nil
	}

	headers, ok := headerField.Interface().(http.Header)
	if !ok || len(headers) == 0 {
		return nil
	}

	return map[string]any{
		"header": headers,
	}
}
