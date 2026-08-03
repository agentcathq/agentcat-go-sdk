// Package registry maps live MCP server objects to their AgentCatInstance.
// Keys are pointer addresses paired with runtime.AddCleanup, so entries are
// released when the server object is garbage collected — required for the
// per-request factory topology, where Track() runs on every request.
package registry

import (
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"

	"go.agentcat.com/sdk/v2/internal/core"
	"go.agentcat.com/sdk/v2/internal/logging"
)

type entry struct {
	token    uint64
	instance *core.AgentCatInstance
}

var (
	serverMap    = make(map[uintptr]entry)
	registryMu   sync.RWMutex
	tokenCounter atomic.Uint64
	logger       = logging.New()
)

type cleanupArg struct {
	key   uintptr
	token uint64
}

// Register stores the AgentCat instance for a server and arranges cleanup
// when the server object becomes unreachable. The token guards against
// address reuse: a new server allocated at a recycled address must not be
// deleted by the old object's pending cleanup.
func Register[T any](server *T, instance *core.AgentCatInstance) {
	if server == nil {
		panic("registry: server must not be nil")
	}
	key := reflect.ValueOf(server).Pointer()
	token := tokenCounter.Add(1)

	registryMu.Lock()
	serverMap[key] = entry{token: token, instance: instance}
	logger.Debugf("Registry: registered %T; map now contains %d entries", server, len(serverMap))
	registryMu.Unlock()

	runtime.AddCleanup(server, func(arg cleanupArg) {
		registryMu.Lock()
		if e, ok := serverMap[arg.key]; ok && e.token == arg.token {
			delete(serverMap, arg.key)
		}
		registryMu.Unlock()
	}, cleanupArg{key: key, token: token})
}

// Get retrieves the instance for a server. Non-pointer values return nil.
func Get(server any) *core.AgentCatInstance {
	if !isPointerKey(server) {
		return nil
	}
	key := reflect.ValueOf(server).Pointer()
	registryMu.RLock()
	defer registryMu.RUnlock()
	return serverMap[key].instance
}

// Unregister removes a server eagerly (before GC would).
func Unregister(server any) {
	if !isPointerKey(server) {
		return
	}
	key := reflect.ValueOf(server).Pointer()
	registryMu.Lock()
	delete(serverMap, key)
	registryMu.Unlock()
}

// Count reports the number of live entries (test hook).
func Count() int {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return len(serverMap)
}

func isPointerKey(server any) bool {
	return server != nil && reflect.ValueOf(server).Kind() == reflect.Ptr
}
