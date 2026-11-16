package agentcat

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestServerInfoTriggerForTool(t *testing.T) {
	if got := ServerInfoTriggerForTool(GetMoreToolsName); got != ServerInfoTriggerFeedback {
		t.Errorf("get_more_tools trigger = %v, want feedback", got)
	}
	if got := ServerInfoTriggerForTool("alpha"); got != ServerInfoTriggerToolCall {
		t.Errorf("alpha trigger = %v, want tool call", got)
	}
}

func TestMaybeStartServerInfo_StartsOnlyWhenDueAndEnabled(t *testing.T) {
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

	restore := SetServerInfoEnabledForTest(false)
	MaybeStartServerInfo(context.Background(), ServerInfoTriggerToolsList, instance, "demo", load)
	restore()
	if !WaitServerInfoForTest(5*time.Second) || posts.Load() != 0 {
		t.Fatalf("POSTs = %d with no instance or the heartbeat off, want 0", posts.Load())
	}

	MaybeStartServerInfo(context.Background(), ServerInfoTriggerToolsList, instance, "demo", load)
	MaybeStartServerInfo(context.Background(), ServerInfoTriggerToolsList, instance, "demo", load)
	if !WaitServerInfoForTest(5*time.Second) || posts.Load() != 1 {
		t.Fatalf("POSTs = %d, want 1", posts.Load())
	}
}
