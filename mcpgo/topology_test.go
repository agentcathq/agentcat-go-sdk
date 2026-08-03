package mcpgo

import (
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/mark3labs/mcp-go/server"
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
func trackThrowawayServer(t *testing.T) weak.Pointer[server.MCPServer] {
	t.Helper()
	s, _ := CreateFullServer()
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
// instance itself (the v1 ServerRef field did, which defeated this entirely)
// and not any closure stored on it: RebuildTools drives a synthetic tools/list
// through this very server, so it must hold it weakly.
func TestTrackedServerIsReleasedWhenUnreachable(t *testing.T) {
	wp := trackThrowawayServer(t)

	if !awaitCollected(wp, 10*time.Second) {
		t.Fatal("the registry pinned the tracked server: runtime.AddCleanup can never fire, " +
			"so a per-request Track() factory leaks one registry entry per request")
	}
}

// TestLiveTrackedServerSurvivesGC is the other half of the contract: a server
// the customer still holds keeps its registry entry across any number of
// collections, and its rebuild hook still reaches it.
func TestLiveTrackedServerSurvivesGC(t *testing.T) {
	s, _ := CreateFullServer()
	if _, err := Track(s, "proj_topology_live", &Options{DisableTracing: true}); err != nil {
		t.Fatalf("Track: %v", err)
	}
	t.Cleanup(func() { unregisterServer(s) })

	for range 5 {
		runtime.GC()
	}

	instance := getMCPcat(s)
	if instance == nil {
		t.Fatal("a live tracked server must keep its registry entry across GC")
	}
	// The weakly-held server must still be reachable through the rebuild hook.
	if instance.RebuildTools == nil {
		t.Fatal("RebuildTools was never stashed")
	}
	if _, err := instance.RebuildTools(t.Context(), nil); err != nil {
		t.Fatalf("RebuildTools on a live server: %v", err)
	}
	if instance.Registries.Load() == nil {
		t.Error("the rebuild hook must still reach the live server and store its registries")
	}
	runtime.KeepAlive(s)
}

// TestRebuildHookStandsDownOnACollectedServer pins the weak hook's degenerate
// case: once the server is gone the hook must return quietly rather than
// resurrect it or panic. It can only be reached at all if some caller still
// holds the instance after the server was collected.
func TestRebuildHookStandsDownOnACollectedServer(t *testing.T) {
	instance := &agentcat.AgentCatInstance{ProjectID: "proj_topology_dead", Options: &agentcat.Options{}}
	wp := stashRebuildOnThrowaway(t, instance)

	if !awaitCollected(wp, 10*time.Second) {
		t.Fatal("the rebuild hook pinned its server")
	}
	tools, err := instance.RebuildTools(t.Context(), nil)
	if err != nil {
		t.Errorf("rebuild on a collected server: %v", err)
	}
	if tools != nil {
		t.Errorf("rebuild on a collected server returned %v, want nil", tools)
	}
}

//go:noinline
func stashRebuildOnThrowaway(t *testing.T, instance *agentcat.AgentCatInstance) weak.Pointer[server.MCPServer] {
	t.Helper()
	s, _ := CreateFullServer()
	stashRebuild(instance, s)
	if instance.RebuildTools == nil {
		t.Fatal("stashRebuild did not install the hook")
	}
	return weak.Make(s)
}
