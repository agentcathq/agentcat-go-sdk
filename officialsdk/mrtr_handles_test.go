//go:build !gosdk_legacy

package officialsdk

// The tests in this file construct MCP multi-round-trip values
// (mcp.InputResponseMap, CallToolResult.NeedsInput) that arrived in go-sdk
// v1.7.0 as part of the 2026-07-28 protocol. They are types in composite
// literals, so unlike the adapter's own MRTR handling they cannot be reached
// through compat.go's feature detection.
//
// The gosdk_legacy build tag excludes them when the module is built against an
// older go-sdk, where MRTR does not exist and there is nothing to assert. The
// CI compatibility matrix passes the tag for every version below v1.7.0;
// ordinary builds keep the coverage.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMRTRInputRequiredTagsAndSkipsDecoration(t *testing.T) {
	// The go-sdk keeps resultType unexported; wire bytes are the sanctioned
	// way to construct an input-required result outside the mcp package.
	inputRequired := &mcp.CallToolResult{}
	if err := json.Unmarshal([]byte(`{"resultType":"input_required","content":[]}`), inputRequired); err != nil {
		t.Fatalf("build input-required result: %v", err)
	}
	if !inputRequired.NeedsInput() {
		t.Fatal("fixture result must report NeedsInput")
	}

	mock := &mockPublisher{}
	handler, _ := buildDirectCallHandler(t, nil, mock, func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method == "tools/list" {
			return &mcp.ListToolsResult{}, nil
		}
		return inputRequired, nil
	})

	// Call WITHOUT session_id: minted — but the intermediate round must not be
	// decorated; the completing round carries the mint-back.
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name:      "interactive_tool",
		Arguments: json.RawMessage(`{}`),
	}}
	res, err := handler(context.Background(), "tools/call", req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	got, ok := res.(*mcp.CallToolResult)
	if !ok {
		t.Fatalf("result = %T", res)
	}
	if got != inputRequired {
		t.Error("input-required results must pass through undecorated (same object)")
	}
	for _, c := range got.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, "MCP INSTRUCTIONS") {
			t.Error("no mint-back on MRTR intermediate results")
		}
	}

	evts := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(evts) == 0 {
		t.Fatal("intermediate round must still publish its event")
	}
	if (*evts[0].Tags)["agentcat_mrtr"] != "input_required" {
		t.Errorf("mrtr tag = %v, want input_required", *evts[0].Tags)
	}
}

func TestMRTRContinuationTagged(t *testing.T) {
	mock := &mockPublisher{}
	handler, _ := buildDirectCallHandler(t, nil, mock, func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method == "tools/list" {
			return &mcp.ListToolsResult{}, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil
	})

	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Name:           "interactive_tool",
		Arguments:      json.RawMessage(`{"session_id":"` + sid("mrtr") + `"}`),
		InputResponses: mcp.InputResponseMap{"r1": &mcp.ElicitResult{Action: "accept"}},
	}}
	if _, err := handler(context.Background(), "tools/call", req); err != nil {
		t.Fatalf("handler: %v", err)
	}

	evts := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(evts) == 0 {
		t.Fatal("no tool-call event published")
	}
	if (*evts[0].Tags)["agentcat_mrtr"] != "continuation" {
		t.Errorf("mrtr tag = %v, want continuation", *evts[0].Tags)
	}
	if evts[0].GetSessionId() != sid("mrtr") {
		t.Errorf("continuation keeps its supplied session, got %q", evts[0].GetSessionId())
	}
}
