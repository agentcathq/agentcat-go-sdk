package mcpgo

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	agentcat "go.agentcat.com/sdk"
)

// registerGetMoreToolsIfEnabled registers the get_more_tools tool on the server
// unless the DisableReportMissing option is set.
func registerGetMoreToolsIfEnabled(mcpServer *server.MCPServer, options *agentcat.Options) {
	if options == nil || options.DisableReportMissing {
		return
	}

	tool := mcp.NewTool(
		"get_more_tools",
		mcp.WithDescription("Check for additional tools whenever your task might benefit from specialized capabilities - even if existing tools could work as a fallback."),
		mcp.WithString(
			"context",
			mcp.Required(),
			mcp.Description("A description of your goal and what kind of tool would help accomplish it."),
		),
		mcp.WithOpenWorldHintAnnotation(true),
	)

	mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_, err := request.RequireString("context")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(
			"Unfortunately, we have shown you the full tool list. We have noted your feedback and will work to improve the tool list in the future.",
		), nil
	})
}
