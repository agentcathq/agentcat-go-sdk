package agentcat

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain replaces the heartbeat's sender before any test runs, so no test
// of this package sends a heartbeat to the network.
func TestMain(m *testing.M) {
	SetServerInfoSenderForTest(func(context.Context, *ServerInfoRequest) (int, error) {
		return 0, errors.New("tests send no heartbeat to the network")
	})
	os.Exit(m.Run())
}

func TestServerInfoTriggerForTool(t *testing.T) {
	if got := ServerInfoTriggerForTool(GetMoreToolsName); got != ServerInfoTriggerFeedback {
		t.Errorf("get_more_tools trigger = %v, want feedback", got)
	}
	if got := ServerInfoTriggerForTool("alpha"); got != ServerInfoTriggerToolCall {
		t.Errorf("alpha trigger = %v, want tool call", got)
	}
}

func TestMaybeStartServerInfo_StartsOnlyWhenDue(t *testing.T) {
	var posts atomic.Int32
	t.Cleanup(SetServerInfoSenderForTest(func(context.Context, *ServerInfoRequest) (int, error) {
		posts.Add(1)
		return 202, nil
	}))
	ResetServerInfoForTest()
	t.Cleanup(ResetServerInfoForTest)

	load := func() (*ServerInfoSource, error) {
		return &ServerInfoSource{Tools: func(context.Context, string) (ServerInfoPage, error) {
			return ServerInfoPage{Items: []any{map[string]any{"name": "alpha"}}}, nil
		}}, nil
	}
	instance := &AgentCatInstance{ProjectID: "proj_root", Options: &Options{}}

	MaybeStartServerInfo(context.Background(), ServerInfoTriggerToolsList, nil, "demo", load)
	if !WaitServerInfoForTest(5*time.Second) || posts.Load() != 0 {
		t.Fatalf("POSTs = %d with no instance, want 0", posts.Load())
	}

	MaybeStartServerInfo(context.Background(), ServerInfoTriggerToolsList, instance, "demo", load)
	MaybeStartServerInfo(context.Background(), ServerInfoTriggerToolsList, instance, "demo", load)
	if !WaitServerInfoForTest(5*time.Second) || posts.Load() != 1 {
		t.Fatalf("POSTs = %d, want 1", posts.Load())
	}
}
