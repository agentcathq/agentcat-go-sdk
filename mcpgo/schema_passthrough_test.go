package mcpgo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestUnparseableInputSchemaIsAdvertisedUntouched pins that a tool whose input
// schema this SDK cannot read keeps its own contract. `true` is a legal JSON
// Schema meaning "accept anything"; parsing it as an object fails, and the
// pipeline must NOT then treat the tool as schemaless and hand the agent a
// synthesised `{"type":"object", …}` in its place.
func TestUnparseableInputSchemaIsAdvertisedUntouched(t *testing.T) {
	mcpServer := server.NewMCPServer("opaque-schema-server", "1.0.0",
		server.WithToolCapabilities(true),
	)
	mcpServer.AddTool(
		mcp.NewToolWithRawSchema("anything_goes",
			"Accepts any arguments at all",
			json.RawMessage(`true`),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(string(echoRawArgs(request))), nil
		},
	)

	h := newSpyHarnessOn(t, mcpServer, DefaultOptions(), "proj_test")

	advertised := listToolRawJSON(t, mcpServer, "anything_goes")
	if advertised != "true" {
		t.Fatalf("unparseable input schema must be advertised verbatim, got %s", advertised)
	}

	// The call still works and is still tracked; only injection stands down.
	result, evt := h.call("anything_goes", map[string]any{"free": "form"})
	if result.IsError {
		t.Fatalf("call on an opaque-schema tool failed: %q", resultText(result))
	}
	if got := resultText(result); !strings.Contains(got, `"free"`) {
		t.Errorf("handler lost the customer's argument: %s", got)
	}
	if evt.GetResourceName() != "anything_goes" {
		t.Errorf("event resource = %q, want anything_goes", evt.GetResourceName())
	}
}

// TestUnparseableOutputSchemaIsAdvertisedUntouched is the output half: an
// output schema the SDK cannot read is neither rewritten nor mirrored into.
func TestUnparseableOutputSchemaIsAdvertisedUntouched(t *testing.T) {
	mcpServer := server.NewMCPServer("opaque-output-server", "1.0.0",
		server.WithToolCapabilities(true),
	)
	mcpServer.AddTool(
		mcp.NewTool("opaque_output",
			mcp.WithDescription("Declares an output schema this SDK cannot read"),
			mcp.WithRawOutputSchema(json.RawMessage(`true`)),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}}, nil
		},
	)

	h := newSpyHarnessOn(t, mcpServer, DefaultOptions(), "proj_test")
	// Driven through the server's own entry point: mcp-go's typed CLIENT
	// cannot decode a boolean outputSchema at all, which is beside the point
	// here — what matters is that the server advertises it unchanged.
	listToolRawJSON(t, mcpServer, "opaque_output")

	registered := mcpServer.ListTools()["opaque_output"]
	if got := string(registered.Tool.RawOutputSchema); got != "true" {
		t.Errorf("registered output schema must not be rewritten, got %s", got)
	}

	res, _ := h.call("opaque_output", map[string]any{"session_id": sid("o")})
	sc, _ := res.StructuredContent.(map[string]any)
	if _, has := sc["mcp_session"]; has {
		t.Errorf("a schema the SDK cannot read must not be mirrored into: %v", sc)
	}
}

// TestRegistriesMergeAcrossPaginatedLists is the regression gate for
// last-write-wins registries: a paginated tools/list describes one page, and
// the tools on every other page must keep the entries their own page
// recorded. Without the merge, calling a page-one tool after listing page two
// hands the customer's handler AgentCat's injected arguments.
func TestRegistriesMergeAcrossPaginatedLists(t *testing.T) {
	mcpServer := server.NewMCPServer("paginated-server", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithPaginationLimit(1),
	)
	for _, name := range []string{"a_first", "b_second"} {
		mcpServer.AddTool(
			mcp.NewTool(name,
				mcp.WithDescription("Echoes the arguments the handler received"),
				mcp.WithString("p", mcp.Description("payload")),
			),
			func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				encoded, err := json.Marshal(request.GetArguments())
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(string(encoded)), nil
			},
		)
	}

	h := newSpyHarnessOn(t, mcpServer, DefaultOptions(), "proj_test")

	// mcp-go's own client follows every page, so a real agent's single
	// ListTools call fires the injection hook once per page.
	page1, err := h.Client.ListToolsByPage(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListToolsByPage(page 1): %v", err)
	}
	if len(page1.Tools) != 1 || page1.Tools[0].Name != "a_first" {
		t.Fatalf("expected page 1 to hold only a_first, got %v", toolNames(page1.Tools))
	}
	if page1.NextCursor == "" {
		t.Fatal("expected a next cursor after page 1")
	}

	req := mcp.ListToolsRequest{}
	req.Params.Cursor = page1.NextCursor
	page2, err := h.Client.ListToolsByPage(context.Background(), req)
	if err != nil {
		t.Fatalf("ListToolsByPage(page 2): %v", err)
	}
	if len(page2.Tools) != 1 || page2.Tools[0].Name != "b_second" {
		t.Fatalf("expected page 2 to hold only b_second, got %v", toolNames(page2.Tools))
	}

	// The page-1 tool must still strip: its registry entry has to survive the
	// page-2 list.
	result, _ := h.call("a_first", map[string]any{
		"p": "keep", "session_id": sid("abc"), "context": "why",
	})
	echoed := decodeEchoedArgs(t, result)
	for _, name := range []string{"session_id", "context"} {
		if _, has := echoed[name]; has {
			t.Errorf("page-1 tool stopped stripping %s after page 2 was listed: %v", name, echoed)
		}
	}
	if echoed["p"] != "keep" {
		t.Errorf("real argument lost: %v", echoed)
	}

	// And the page-2 tool strips too.
	result2, _ := h.call("b_second", map[string]any{
		"p": "keep", "session_id": sid("abc"), "context": "why",
	})
	echoed2 := decodeEchoedArgs(t, result2)
	for _, name := range []string{"session_id", "context"} {
		if _, has := echoed2[name]; has {
			t.Errorf("page-2 tool did not strip %s: %v", name, echoed2)
		}
	}
}

// toolNames renders a listed tool slice for failure messages.
func toolNames(tools []mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

// TestRegistriesMergeAcrossFilteredLists covers the same failure through a
// tool filter: a list that hides a tool must not stop that tool from
// stripping when it is later unhidden and called.
func TestRegistriesMergeAcrossFilteredLists(t *testing.T) {
	var hideSecond bool
	mcpServer := server.NewMCPServer("filtered-server", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithToolFilter(func(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
			if !hideSecond {
				return tools
			}
			kept := tools[:0:0]
			for _, tool := range tools {
				if tool.Name != "b_second" {
					kept = append(kept, tool)
				}
			}
			return kept
		}),
	)
	for _, name := range []string{"a_first", "b_second"} {
		mcpServer.AddTool(
			mcp.NewTool(name,
				mcp.WithDescription("Echoes the arguments the handler received"),
				mcp.WithString("p", mcp.Description("payload")),
			),
			func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				encoded, err := json.Marshal(request.GetArguments())
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(string(encoded)), nil
			},
		)
	}

	h := newSpyHarnessOn(t, mcpServer, DefaultOptions(), "proj_test")

	h.listTools() // both tools visible: both recorded
	hideSecond = true
	h.listTools() // b_second hidden: its entry must survive
	hideSecond = false

	result, _ := h.call("b_second", map[string]any{
		"p": "keep", "session_id": sid("abc"), "context": "why",
	})
	echoed := decodeEchoedArgs(t, result)
	for _, name := range []string{"session_id", "context"} {
		if _, has := echoed[name]; has {
			t.Errorf("a filtered-out tool stopped stripping %s: %v", name, echoed)
		}
	}
}
