package officialsdk

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestConcurrentToolsList_ContextInjection_NoSharedToolMutation hammers
// tools/list concurrently with context-param injection enabled. The go-sdk's
// ListToolsResult carries pointers to the server's shared registered Tool
// objects: injection must copy, never mutate them, or concurrent requests
// race (fatally, when the nested properties map is written concurrently).
func TestConcurrentToolsList_ContextInjection_NoSharedToolMutation(t *testing.T) {
	opts := DefaultOptions()
	opts.DisableReportMissing = true
	// Context injection enabled (DisableToolCallContext = false).

	clientSession, _, _ := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	const workers = 8
	const listsPerWorker = 10
	var wg sync.WaitGroup
	var withContext atomic.Int64

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range listsPerWorker {
				result, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{})
				if err != nil {
					t.Errorf("ListTools error: %v", err)
					return
				}
				for _, tool := range result.Tools {
					schema, ok := tool.InputSchema.(map[string]any)
					if !ok {
						continue
					}
					if props, ok := schema["properties"].(map[string]any); ok {
						if _, has := props["context"]; has {
							withContext.Add(1)
						}
					}
				}
			}
		}()
	}
	wg.Wait()

	if withContext.Load() == 0 {
		t.Error("expected injected context params in concurrent tools/list responses")
	}
}

// TestConcurrentIdentifyChanges_NoRace hammers tool calls while the Identify
// callback keeps returning different identities with UserData maps, verifying
// (under -race) that identity merge, session mutation, and identify-event
// publishing in the async capture goroutines are properly synchronized.
func TestConcurrentIdentifyChanges_NoRace(t *testing.T) {
	var n atomic.Int64
	opts := DefaultOptions()
	opts.DisableReportMissing = true
	opts.DisableToolCallContext = true
	opts.Identify = func(ctx context.Context, request mcp.Request) *UserIdentity {
		i := n.Add(1)
		return &UserIdentity{
			UserID:   fmt.Sprintf("user-%d", i%4),
			UserName: "racer",
			UserData: map[string]any{"call": i, "nested": map[string]any{"k": i}},
		}
	}

	clientSession, _, mock := setupStreamableHTTP(t, opts)
	ctx := context.Background()

	const workers = 8
	const callsPerWorker = 10
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range callsPerWorker {
				_, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
					Name:      "add_todo",
					Arguments: map[string]any{"title": fmt.Sprintf("todo-%d", j)},
				})
				if err != nil {
					t.Errorf("CallTool error: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	identifies := waitForEventType(mock, "agentcat:identify", 1, 5*time.Second)
	if len(identifies) == 0 {
		t.Error("expected at least one identify event from changing identities")
	}
}
