package officialsdk

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// extractResponse extracts response data for the published event. v2 captures
// tool calls only; the recorded response is the customer's ORIGINAL result,
// never the decorated wire copy.
func extractResponse(method string, result mcp.Result) map[string]any {
	if result == nil {
		return nil
	}

	resp := make(map[string]any)

	if method == "tools/call" {
		if r, ok := result.(*mcp.CallToolResult); ok {
			if r.StructuredContent != nil {
				resp["structuredContent"] = agentcat.ConvertToMap(r.StructuredContent)
			}
			// The content list is always recorded, even empty: tokens.OutputTokens
			// treats a missing "content" key as "no content list" and falls back to
			// counting the whole response (structuredContent included). The wire
			// carries `[]` for an empty Content slice too, so this matches what the
			// customer's own client sees.
			if len(r.Content) > 0 {
				resp["content"] = agentcat.ConvertToMap(r.Content)
			} else {
				resp["content"] = []any{}
			}
			resp["isError"] = r.IsError
		}
	}

	if len(resp) == 0 {
		return nil
	}
	return resp
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
