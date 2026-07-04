package officialsdk

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk"
)

// extractParameters extracts parameters from the request based on its method.
func extractParameters(method string, req mcp.Request) map[string]any {
	if req == nil {
		return nil
	}

	params := make(map[string]any)

	switch method {
	case "tools/call":
		if toolReq, ok := req.(*mcp.CallToolRequest); ok && toolReq.Params != nil {
			params["name"] = toolReq.Params.Name
			args := unmarshalArguments(toolReq.Params.Arguments)
			if args != nil {
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
		}

	case "resources/read":
		if resReq, ok := req.(*mcp.ReadResourceRequest); ok && resReq.Params != nil {
			params["uri"] = resReq.Params.URI
		}

	case "prompts/get":
		if promptReq, ok := req.(*mcp.GetPromptRequest); ok && promptReq.Params != nil {
			params["name"] = promptReq.Params.Name
			if len(promptReq.Params.Arguments) > 0 {
				params["arguments"] = promptReq.Params.Arguments
			}
		}

	case "initialize":
		// On the server side, initialize is received as ServerRequest[*InitializeParams].
		// Note: mcp.InitializeRequest is a ClientRequest alias, not what the server receives.
		if initReq, ok := req.(*mcp.ServerRequest[*mcp.InitializeParams]); ok && initReq.Params != nil {
			params["protocolVersion"] = initReq.Params.ProtocolVersion
			if initReq.Params.ClientInfo != nil {
				params["clientInfo"] = agentcat.ConvertToMap(initReq.Params.ClientInfo)
			}
		}
	}

	if len(params) == 0 {
		return nil
	}
	return params
}

// extractResponse extracts response data based on the method and result type.
func extractResponse(method string, result mcp.Result) map[string]any {
	if result == nil {
		return nil
	}

	resp := make(map[string]any)

	switch method {
	case "tools/call":
		if r, ok := result.(*mcp.CallToolResult); ok {
			if r.StructuredContent != nil {
				resp["structuredContent"] = agentcat.ConvertToMap(r.StructuredContent)
			}
			if len(r.Content) > 0 {
				resp["content"] = agentcat.ConvertToMap(r.Content)
			}
			resp["isError"] = r.IsError
		}

	case "resources/read":
		if r, ok := result.(*mcp.ReadResourceResult); ok {
			if len(r.Contents) > 0 {
				resp["contents"] = agentcat.ConvertToMap(r.Contents)
			}
		}

	case "prompts/get":
		if r, ok := result.(*mcp.GetPromptResult); ok {
			resp["description"] = r.Description
			if len(r.Messages) > 0 {
				resp["messages"] = agentcat.ConvertToMap(r.Messages)
			}
		}

	case "tools/list":
		if r, ok := result.(*mcp.ListToolsResult); ok {
			if len(r.Tools) > 0 {
				resp["tools"] = agentcat.ConvertToMap(r.Tools)
			}
		}

	case "initialize":
		if r, ok := result.(*mcp.InitializeResult); ok {
			resp["protocolVersion"] = r.ProtocolVersion
			if r.ServerInfo != nil {
				resp["serverInfo"] = agentcat.ConvertToMap(r.ServerInfo)
			}
		}
	}

	if len(resp) == 0 {
		return nil
	}
	return resp
}

// extractToolName extracts the tool name from a tool call request.
func extractToolName(req mcp.Request) string {
	if toolReq, ok := req.(*mcp.CallToolRequest); ok && toolReq.Params != nil {
		return toolReq.Params.Name
	}
	return ""
}

// extractResourceURI extracts the resource URI from a resource read request.
func extractResourceURI(req mcp.Request) string {
	if resReq, ok := req.(*mcp.ReadResourceRequest); ok && resReq.Params != nil {
		return resReq.Params.URI
	}
	return ""
}

// unmarshalArguments converts json.RawMessage arguments to map[string]any.
func unmarshalArguments(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil
	}
	return args
}

// extractExtra extracts transport-layer metadata from the request.
// For HTTP transports, this includes headers and OAuth token info.
// Returns nil if no extra data is available (e.g., stdio transport or ClientRequest).
func extractExtra(req mcp.Request) map[string]any {
	if req == nil {
		return nil
	}

	re := req.GetExtra()
	if re == nil {
		return nil
	}

	extra := make(map[string]any)

	if len(re.Header) > 0 {
		extra["header"] = re.Header
	}

	// Serialize TokenInfo generically so we capture all fields (including any
	// added in newer go-sdk versions) without referencing them directly.
	if re.TokenInfo != nil {
		if tokenInfo := agentcat.ConvertToMap(re.TokenInfo); tokenInfo != nil {
			extra["tokenInfo"] = tokenInfo
		}
	}

	if len(extra) == 0 {
		return nil
	}
	return extra
}
