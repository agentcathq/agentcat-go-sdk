//go:build mcpgo_legacy

package mcpgo

import "github.com/mark3labs/mcp-go/mcp"

// toolTitleOptions returns no options: mcp-go before v0.54 has no top-level
// tool title, so the fixture carries only the annotation title.
func toolTitleOptions(string) []mcp.ToolOption {
	return nil
}
