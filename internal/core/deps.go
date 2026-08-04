package core

import "runtime/debug"

// SDKModulePath is this SDK's root module path, used to resolve its own version.
const SDKModulePath = "go.agentcat.com/sdk/v2"

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
