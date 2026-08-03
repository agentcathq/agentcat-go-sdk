package core

import "testing"

func TestGetDependencyVersion_KnownDep(t *testing.T) {
	// ksuid is a direct dependency of the root module, so it should be in build info.
	version := GetDependencyVersion("github.com/segmentio/ksuid")
	if version == "dev" {
		t.Skip("build info not available (binary not built with module support)")
	}
	if version == "" {
		t.Error("expected non-empty version for known dependency")
	}
}

func TestGetDependencyVersion_UnknownDep(t *testing.T) {
	version := GetDependencyVersion("github.com/nonexistent/package")
	if version != "dev" {
		t.Errorf("expected \"dev\" for unknown dependency, got %q", version)
	}
}
