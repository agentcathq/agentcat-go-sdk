package mcpgo

import (
	"net/http"
	"reflect"

	"github.com/mark3labs/mcp-go/mcp"
	agentcat "go.agentcat.com/sdk"
)

// extractUserIntentFromRequest extracts the context parameter from a tool call request.
func extractUserIntentFromRequest(request any) string {
	if toolReq, ok := request.(*mcp.CallToolRequest); ok {
		if args := toolReq.GetArguments(); args != nil {
			if contextVal, ok := args["context"].(string); ok {
				return contextVal
			}
		}
	}
	return ""
}

// extractParameters extracts parameters from the request based on its type.
func extractParameters(request any) map[string]any {
	params := make(map[string]any)

	switch req := request.(type) {
	case *mcp.CallToolRequest:
		params["name"] = req.Params.Name
		if args := req.GetArguments(); args != nil {
			filteredArgs := make(map[string]any)
			for k, v := range args {
				if k != "context" {
					filteredArgs[k] = v
				}
			}
			if len(filteredArgs) > 0 {
				params["arguments"] = filteredArgs
			}
		}
	case *mcp.ReadResourceRequest:
		params["uri"] = req.Params.URI
	case *mcp.GetPromptRequest:
		params["name"] = req.Params.Name
		if len(req.Params.Arguments) > 0 {
			params["arguments"] = req.Params.Arguments
		}
	case *mcp.InitializeRequest:
		params["protocolVersion"] = req.Params.ProtocolVersion
		params["clientInfo"] = agentcat.ConvertToMap(req.Params.ClientInfo)
	}

	if len(params) == 0 {
		return nil
	}
	return params
}

// extractResponse extracts response data based on the response type.
func extractResponse(response any) map[string]any {
	resp := make(map[string]any)

	switch r := response.(type) {
	case *mcp.CallToolResult:
		if r.StructuredContent != nil {
			resp["structuredContent"] = agentcat.ConvertToMap(r.StructuredContent)
		}
		if len(r.Content) > 0 {
			resp["content"] = agentcat.ConvertToMap(r.Content)
		}
		resp["isError"] = r.IsError
	case *mcp.ReadResourceResult:
		if len(r.Contents) > 0 {
			resp["contents"] = agentcat.ConvertToMap(r.Contents)
		}
	case *mcp.GetPromptResult:
		resp["description"] = r.Description
		if len(r.Messages) > 0 {
			resp["messages"] = agentcat.ConvertToMap(r.Messages)
		}
	case *mcp.InitializeResult:
		resp["protocolVersion"] = r.ProtocolVersion
		resp["serverInfo"] = agentcat.ConvertToMap(r.ServerInfo)
	case *mcp.ListToolsResult:
		if len(r.Tools) > 0 {
			resp["tools"] = agentcat.ConvertToMap(r.Tools)
		}
	}

	if len(resp) == 0 {
		return nil
	}
	return resp
}

// extractResourceName extracts the resource URI from a resource read request.
func extractResourceName(request any) string {
	if resourceReq, ok := request.(*mcp.ReadResourceRequest); ok {
		return resourceReq.Params.URI
	}
	return ""
}

// extractToolName extracts the tool name from a tool call request.
func extractToolName(request any) string {
	if toolReq, ok := request.(*mcp.CallToolRequest); ok {
		return toolReq.Params.Name
	}
	return ""
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
