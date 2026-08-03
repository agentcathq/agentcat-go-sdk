package officialsdk

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// registerGetMoreToolsIfEnabled registers the get_more_tools tool on the server
// unless the DisableReportMissing option is set.
func registerGetMoreToolsIfEnabled(mcpServer *mcp.Server, options *agentcat.Options) {
	if options == nil || options.DisableReportMissing {
		return
	}

	tool := &mcp.Tool{
		Name:        agentcat.GetMoreToolsName,
		Description: agentcat.GetMoreToolsDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"context": map[string]any{
					"type":        "string",
					"description": agentcat.GetMoreToolsContextDescription,
				},
			},
			"required": []string{"context"},
		},
	}

	mcpServer.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Validate that "context" argument is provided
		args := unmarshalArguments(req.Params.Arguments)
		if _, ok := args["context"].(string); !ok {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: "context is a required argument"},
				},
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: agentcat.GetMoreToolsResponseText,
				},
			},
		}, nil
	})
}
