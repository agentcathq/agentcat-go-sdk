//go:build !gosdk_legacy

package officialsdk

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestCompatShimsResolveAgainstPinnedVersion is the tripwire for compat.go.
//
// Every shim there is deliberately silent when its API is missing — that is
// what lets the adapter build against go-sdk back to v1.4.1. The failure mode
// is that an upstream RENAME is indistinguishable from an old version: client
// identity would quietly fall back to a ladder reporting nothing, and the
// agentcat_mrtr tag would vanish, with the whole suite still green.
//
// It therefore asserts PRESENCE, which is only meaningful on the version
// go.mod pins. The compatibility matrix deliberately builds against older
// go-sdk releases where absence is the expected, correct answer, so it passes
// gosdk_legacy and this file drops out. Behavior on those versions is covered
// by the untagged tests, which gate on capability rather than presence.
func TestCompatShimsResolveAgainstPinnedVersion(t *testing.T) {
	var ctr *mcp.CallToolRequest
	if _, ok := any(ctr).(interface{ ProtocolVersion() string }); !ok {
		t.Error("CallToolRequest.ProtocolVersion() not found on the pinned go-sdk: " +
			"the accessor was renamed, and protocolVersionOf is now silently falling " +
			"back to the _meta ladder")
	}
	if _, ok := any(ctr).(interface{ ClientInfo() *mcp.Implementation }); !ok {
		t.Error("CallToolRequest.ClientInfo() not found on the pinned go-sdk: the " +
			"accessor was renamed or retyped, and clientInfoOf is now silently falling " +
			"back to the _meta ladder")
	}

	var res *mcp.CallToolResult
	if _, ok := any(res).(interface{ NeedsInput() bool }); !ok {
		t.Error("CallToolResult.NeedsInput() not found on the pinned go-sdk: the " +
			"method was renamed, and every MRTR intermediate round is now being " +
			"decorated and tagged as an ordinary call")
	}

	if inputResponsesField() == nil {
		t.Error("CallToolParamsRaw.InputResponses not found on the pinned go-sdk: the " +
			"field was renamed, and MRTR continuations are no longer tagged")
	}
}

func TestResultNeedsInputBeforeWireAnnotation(t *testing.T) {
	annotated := &mcp.CallToolResult{}
	if err := json.Unmarshal([]byte(`{"resultType":"input_required"}`), annotated); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		res  *mcp.CallToolResult
		want bool
	}{
		{name: "nil"},
		{name: "empty", res: &mcp.CallToolResult{}},
		{name: "complete", res: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}},
		{name: "error", res: &mcp.CallToolResult{IsError: true}},
		{name: "input requests", res: &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"roots": &mcp.ListRootsParams{}}}, want: true},
		{name: "empty input requests", res: &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{}}, want: true},
		{name: "request state only", res: &mcp.CallToolResult{RequestState: "next"}, want: true},
		{name: "wire annotation only", res: annotated, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := resultNeedsInput(tt.res); got != tt.want {
				t.Errorf("resultNeedsInput() = %v, want %v", got, tt.want)
			}
		})
	}
}
