//go:build !mcpgo_legacy

package mcpgo

import "testing"

// TestCompatShimsResolveAgainstPinnedVersion is the tripwire for compat.go.
//
// The shims are deliberately silent when a field is absent — that is what lets
// the adapter build against older mcp-go. The failure mode is that an upstream
// RENAME looks identical to an old version, and the adapter would quietly stop
// preserving numeric fidelity with every test still green. This test makes
// that impossible on the version go.mod pins.
func TestCompatShimsResolveAgainstPinnedVersion(t *testing.T) {
	if rawArgumentsField() == nil {
		t.Error("mcp.CallToolParams.RawArguments not found: either the pinned mcp-go " +
			"predates v0.56.0, or the field was renamed and setRawArguments is now " +
			"silently doing nothing")
	}
	if rawStructuredField() == nil {
		t.Error("mcp.CallToolResult.RawStructuredContent not found: either the pinned " +
			"mcp-go predates v0.56.0, or the field was renamed and " +
			"clearRawStructuredContent is now silently doing nothing")
	}
}
