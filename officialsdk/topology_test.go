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

// trackThrowawayServerWithCustomerMiddleware is the factory-topology shape
// that used to leak: customer receiving middleware whose closure captures the
// server, added BEFORE Track. The stashed rebuild chain includes that closure,
// so a strong RebuildTools→chain reference would give the registry a permanent
// path to the server.
//
//go:noinline
func trackThrowawayServerWithCustomerMiddleware(t *testing.T) weak.Pointer[mcp.Server] {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "throwaway-mw", Version: "1.0.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "noop", Description: "does nothing"},
		func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, struct{}, error) {
			return nil, struct{}{}, nil
		})
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			_ = s // the customer's closure holds the server
			return next(ctx, method, req)
		}
	})
	if _, err := Track(s, "proj_topology_mw", &Options{DisableTracing: true}); err != nil {
		t.Fatalf("Track: %v", err)
	}
	// Guard against a vacuous pass: the entry and the stashed rebuild chain
	// must both exist before we ask whether the server is released.
	instance := getMCPcat(s)
	if instance == nil {
		t.Fatal("Track did not register the server")
	}
	if instance.RebuildTools == nil {
		t.Fatal("Track did not stash the rebuild chain")
	}
	return weak.Make(s)
}

// TestTrackedServerWithCustomerMiddlewareIsReleased pins the weak rebuild
// holder: with customer middleware capturing the server in the chain, the
// registry must still release the server once the customer drops it.
func TestTrackedServerWithCustomerMiddlewareIsReleased(t *testing.T) {
	wp := trackThrowawayServerWithCustomerMiddleware(t)

	if !awaitCollected(wp, 10*time.Second) {
		t.Fatal("RebuildTools pinned the handler chain: a customer middleware closure " +
			"holding the server keeps it reachable from the registry forever, so a " +
			"per-request Track() factory leaks one server per request")
	}
}

// stashThrowawayRebuild stashes a rebuild whose holder nothing keeps alive,
// standing in for a collected server (the middleware handler that normally
// owns the holder died with it).
//
//go:noinline
func stashThrowawayRebuild(instance *agentcat.AgentCatInstance) weak.Pointer[rebuildTarget] {
	target := &rebuildTarget{next: func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return &mcp.ListToolsResult{}, nil
	}}
	stashRebuild(instance, target)
	return weak.Make(target)
}

// TestRebuildStandsDownOnCollectedChain: once the holder is gone, RebuildTools
// must degrade to "no tools" — never panic, never resurrect the chain.
func TestRebuildStandsDownOnCollectedChain(t *testing.T) {
	instance := &agentcat.AgentCatInstance{Options: &agentcat.Options{}}
	wp := stashThrowawayRebuild(instance)

	if !awaitCollected(wp, 10*time.Second) {
		t.Fatal("nothing should keep the rebuild holder alive here")
	}
	tools, err := instance.RebuildTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("RebuildTools on a collected chain: %v", err)
	}
	if tools != nil {
		t.Errorf("RebuildTools on a collected chain must stand down, got %+v", tools)
	}
}
