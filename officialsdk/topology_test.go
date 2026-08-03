package officialsdk

import (
	"context"
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// awaitCollected forces collections until wp's referent is unreachable.
// runtime.AddCleanup runs its callbacks asynchronously after a collection
// discovers the object, so one runtime.GC() is not a signal on its own.
func awaitCollected[T any](wp weak.Pointer[T], timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		runtime.GC()
		if wp.Value() == nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// trackThrowawayServer tracks a server and returns only a weak reference to it,
// so no live stack slot in the caller's frame can keep it reachable.
//
//go:noinline
func trackThrowawayServer(t *testing.T) weak.Pointer[mcp.Server] {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "throwaway", Version: "1.0.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "noop", Description: "does nothing"},
		func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, struct{}, error) {
			return nil, struct{}{}, nil
		})
	if _, err := Track(s, "proj_topology", &Options{DisableTracing: true}); err != nil {
		t.Fatalf("Track: %v", err)
	}
	// Guard against a vacuous pass: the entry must exist before we ask whether
	// it is released.
	if getMCPcat(s) == nil {
		t.Fatal("Track did not register the server")
	}
	return weak.Make(s)
}

// TestTrackedServerIsReleasedWhenUnreachable is the registry's whole reason for
// keying on pointer addresses and arming runtime.AddCleanup: a per-request
// Track() factory calls Track once per HTTP request, so an entry that outlives
// its server leaks one map entry per request, forever.
//
// Nothing reachable from the registry may hold the server strongly — not the
// instance (the v1 ServerRef field did, which defeated this entirely) and not
// any closure stored on it (RebuildTools).
func TestTrackedServerIsReleasedWhenUnreachable(t *testing.T) {
	wp := trackThrowawayServer(t)

	if !awaitCollected(wp, 10*time.Second) {
		t.Fatal("the registry pinned the tracked server: runtime.AddCleanup can never fire, " +
			"so a per-request Track() factory leaks one registry entry per request")
	}
}

// TestLiveTrackedServerSurvivesGC is the other half of the contract: a server
// the customer still holds keeps its registry entry across any number of
// collections.
func TestLiveTrackedServerSurvivesGC(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "long-lived", Version: "1.0.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "noop", Description: "does nothing"},
		func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, struct{}, error) {
			return nil, struct{}{}, nil
		})
	if _, err := Track(s, "proj_topology_live", &Options{DisableTracing: true}); err != nil {
		t.Fatalf("Track: %v", err)
	}
	t.Cleanup(func() { agentcat.UnregisterServer(s) })

	for range 5 {
		runtime.GC()
	}
	if getMCPcat(s) == nil {
		t.Error("a live tracked server must keep its registry entry across GC")
	}
	runtime.KeepAlive(s)
}
