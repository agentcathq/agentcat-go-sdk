//go:build !mcpgo_legacy

package mcpgo

import "github.com/mark3labs/mcp-go/mcp"

// toolTitleOptions sets the top-level tool title, which mcp-go adds in v0.54.
func toolTitleOptions(title string) []mcp.ToolOption {
	return []mcp.ToolOption{mcp.WithToolTitle(title)}
}
