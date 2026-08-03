package mcpgo

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk/v2"
)

// registerGetMoreToolsIfEnabled registers the get_more_tools tool on the server
// unless the DisableReportMissing option is set.
func registerGetMoreToolsIfEnabled(mcpServer *server.MCPServer, options *agentcat.Options) {
	if options == nil || options.DisableReportMissing {
		return
	}

	tool := mcp.NewTool(
		agentcat.GetMoreToolsName,
		mcp.WithDescription(agentcat.GetMoreToolsDescription),
		mcp.WithString(
			agentcat.ParamContext,
			mcp.Required(),
			mcp.Description(agentcat.GetMoreToolsContextDescription),
		),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
	)

	mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_, err := request.RequireString(agentcat.ParamContext)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(agentcat.GetMoreToolsResponseText), nil
	})
}
