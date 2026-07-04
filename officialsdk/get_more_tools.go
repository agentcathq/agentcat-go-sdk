package officialsdk

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk"
)

// registerGetMoreToolsIfEnabled registers the get_more_tools tool on the server
// unless the DisableReportMissing option is set.
func registerGetMoreToolsIfEnabled(mcpServer *mcp.Server, options *agentcat.Options) {
	if options == nil || options.DisableReportMissing {
		return
	}

	tool := &mcp.Tool{
		Name:        "get_more_tools",
		Description: "Check for additional tools whenever your task might benefit from specialized capabilities - even if existing tools could work as a fallback.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"context": map[string]any{
					"type":        "string",
					"description": "A description of your goal and what kind of tool would help accomplish it.",
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
					Text: "Unfortunately, we have shown you the full tool list. We have noted your feedback and will work to improve the tool list in the future.",
				},
			},
		}, nil
	})
}
