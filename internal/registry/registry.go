package registry

import (
	"fmt"
	"reflect"
	"sync"

	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
)

var (
	serverMCPcatMap = make(map[any]*core.AgentCatInstance)
	registryMu      sync.RWMutex
	logger          = logging.New()
)

// Register stores the AgentCat instance for a given server.
// The server must be a pointer type (as all MCP server types are).
func Register(server any, instance *core.AgentCatInstance) {
	mustBePointer(server)

	logger.Debugf("Registry: Registering server %T", server)

	registryMu.Lock()
	defer registryMu.Unlock()
	serverMCPcatMap[server] = instance
	logger.Debugf("Registry: Map now contains %d entries", len(serverMCPcatMap))
}

// Get retrieves the AgentCat instance for a given server.
func Get(server any) *core.AgentCatInstance {
	if server == nil {
		return nil
	}

	registryMu.RLock()
	defer registryMu.RUnlock()

	instance := serverMCPcatMap[server]
	if instance == nil {
		logger.Debugf("Registry: No instance found for %T. Map contains %d entries", server, len(serverMCPcatMap))
	}
	return instance
}

// Unregister removes a server from the registry.
func Unregister(server any) {
	if server == nil {
		return
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	delete(serverMCPcatMap, server)
}

// mustBePointer panics if server is nil or not a pointer type. This catches
// misuse at registration time rather than silently mapping all value types
// to the same entry.
func mustBePointer(server any) {
	if server == nil {
		panic("registry: server must not be nil")
	}
	if reflect.ValueOf(server).Kind() != reflect.Ptr {
		panic(fmt.Sprintf("registry: server must be a pointer, got %T", server))
	}
}
