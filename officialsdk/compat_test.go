package officialsdk

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	agentcat "go.agentcat.com/sdk/v2"
)

// acceptsBooleanOutputSchema reports whether this go-sdk registers a tool
// whose output schema is the boolean `true`. v1.7.0 accepts it; earlier
// versions panic inside AddTool. Probing beats version-sniffing — go-sdk
// exposes no version at runtime, and the behavior is what tests care about.
func acceptsBooleanOutputSchema() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil).AddTool(
		&mcp.Tool{
			Name:         "probe",
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			OutputSchema: json.RawMessage(`true`),
		},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { return nil, nil },
	)
	return true
}

// TestClientIdentityFallbackLadder pins the pre-v1.7.0 path itself, on the
// pinned version, so it cannot rot unnoticed between compatibility runs. The
// _meta rung is the one a 2026 client uses and the only rung reachable without
// a live session.
func TestClientIdentityFallbackLadder(t *testing.T) {
	params := &mcp.CallToolParamsRaw{Name: "t"}
	params.SetMeta(map[string]any{
		agentcat.MetaClientInfoKey:      map[string]any{"name": "legacy-client", "version": "3.2"},
		agentcat.MetaProtocolVersionKey: "2025-06-18",
	})

	// A request with no Session: only the _meta rung can answer, which is
	// exactly the shape the fallback must handle without panicking.
	got := clientInfoFromMeta(params)
	if got == nil {
		t.Fatal("the _meta rung must resolve client identity without a session")
	}
	if got.Name != "legacy-client" || got.Version != "3.2" {
		t.Errorf("client identity = %q/%q, want legacy-client/3.2", got.Name, got.Version)
	}
	if pv := protocolVersionFromMeta(params); pv != "2025-06-18" {
		t.Errorf("protocol version = %q, want 2025-06-18", pv)
	}
}
