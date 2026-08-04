package mcpgo

import (
	"context"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"weak"

	"github.com/mark3labs/mcp-go/server"
)

// Per-server capture state and per-Hooks instrumentation state, both keyed by
// pointer address and released via runtime.AddCleanup — the same pattern as
// the root module's registry, reimplemented here because adapters cannot
// import internal/. AgentCat's hook closures outlive any single server
// exactly when a customer shares one *server.Hooks value across servers
// (server.WithHooks), so the closures carry no per-server state and these
// maps are how they find it; neither map may pin what it describes.

// cleanupToken guards a map entry against pointer-address reuse: a new object
// allocated at a recycled address must not be deleted by the old object's
// pending cleanup.
type cleanupToken struct {
	key   uintptr
	token uint64
}

var stateTokens atomic.Uint64

// capturers resolves a live server to its capturer for the shared hook
// closures. Entries hold the capturer WEAKLY: its one strong owner is the
// server's own tool middleware (installed by installTracking), so the
// capturer — and the strong server reference inside it — dies with the
// server instead of being pinned from this global map.
var (
	capturersMu sync.RWMutex
	capturers   = map[uintptr]capturerEntry{}
)

type capturerEntry struct {
	token uint64
	wp    weak.Pointer[capturer]
}

func registerCapturer(s *server.MCPServer, c *capturer) {
	if s == nil || c == nil {
		return
	}
	key := reflect.ValueOf(s).Pointer()
	token := stateTokens.Add(1)

	capturersMu.Lock()
	capturers[key] = capturerEntry{token: token, wp: weak.Make(c)}
	capturersMu.Unlock()

	runtime.AddCleanup(s, func(arg cleanupToken) {
		capturersMu.Lock()
		if e, ok := capturers[arg.key]; ok && e.token == arg.token {
			delete(capturers, arg.key)
		}
		capturersMu.Unlock()
	}, cleanupToken{key: key, token: token})
}

// capturerFor returns the live capturer for a server, or nil for a server
// these hooks were never installed on (an untracked server sharing the same
// Hooks value) or whose capturer is gone.
func capturerFor(s *server.MCPServer) *capturer {
	if s == nil {
		return nil
	}
	key := reflect.ValueOf(s).Pointer()
	capturersMu.RLock()
	e, ok := capturers[key]
	capturersMu.RUnlock()
	if !ok {
		return nil
	}
	c := e.wp.Value()
	// Weak pointers are cleared before their memory is reused, so a live c
	// always belongs to a live registration; the identity check is belt and
	// suspenders against an entry overwritten for a recycled address.
	if c == nil || c.server != s {
		return nil
	}
	return c
}

// capturerFromContext resolves the in-flight request's server to its capturer.
func capturerFromContext(ctx context.Context) *capturer {
	return capturerFor(server.ServerFromContext(ctx))
}

// instrumentedHooks records which *server.Hooks values already carry
// AgentCat's three hook closures. The closures dispatch on the request
// context, so one instrumentation serves every server the customer attaches
// the Hooks value to — appending per Track() would grow the customer's hook
// slices, and every request's hook iteration, forever in the per-request
// factory topology.
var (
	instrumentedMu    sync.Mutex
	instrumentedHooks = map[uintptr]hooksEntry{}
)

type hooksEntry struct {
	token uint64
	wp    weak.Pointer[server.Hooks]
}

// instrumentHooksOnce runs install exactly once per Hooks value. Claim and
// install happen under one lock, so two concurrent Track calls sharing a
// Hooks value cannot interleave their appends.
func instrumentHooksOnce(h *server.Hooks, install func()) {
	if h == nil {
		return
	}
	key := reflect.ValueOf(h).Pointer()

	instrumentedMu.Lock()
	defer instrumentedMu.Unlock()
	if e, ok := instrumentedHooks[key]; ok && e.wp.Value() == h {
		return // already instrumented
	}
	// First claim — or a stale entry left by a collected Hooks at a recycled
	// address, which the weak identity check above detects: record and
	// install. AddCleanup on a non-heap Hooks (a package-level variable) is a
	// no-op, which is the correct semantic — a global Hooks stays instrumented
	// for the life of the process.
	token := stateTokens.Add(1)
	instrumentedHooks[key] = hooksEntry{token: token, wp: weak.Make(h)}
	runtime.AddCleanup(h, func(arg cleanupToken) {
		instrumentedMu.Lock()
		if e, ok := instrumentedHooks[arg.key]; ok && e.token == arg.token {
			delete(instrumentedHooks, arg.key)
		}
		instrumentedMu.Unlock()
	}, cleanupToken{key: key, token: token})
	install()
}
