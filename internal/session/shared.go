package session

import (
	"fmt"
	"runtime/debug"

	"github.com/segmentio/ksuid"
	"go.agentcat.com/sdk/internal/core"
)

// GenerateSessionID generates a new session ID with the MCPCat session prefix.
func GenerateSessionID() string {
	return fmt.Sprintf("%s_%s", core.PrefixSession, ksuid.New().String())
}

// GetDependencyVersion returns the version of the given module from build info,
// or "dev" if the module is not found.
func GetDependencyVersion(modulePath string) string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == modulePath {
				return dep.Version
			}
		}
	}
	return "dev"
}
