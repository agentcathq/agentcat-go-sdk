package session

import (
	"strings"
	"testing"

	"go.agentcat.com/sdk/internal/core"
)

func TestGenerateSessionID_HasPrefix(t *testing.T) {
	id := GenerateSessionID()
	prefix := string(core.PrefixSession) + "_"
	if !strings.HasPrefix(id, prefix) {
		t.Errorf("GenerateSessionID() = %q, want prefix %q", id, prefix)
	}
}

func TestGenerateSessionID_SuffixNonEmpty(t *testing.T) {
	id := GenerateSessionID()
	prefix := string(core.PrefixSession) + "_"
	suffix := strings.TrimPrefix(id, prefix)
	if suffix == "" {
		t.Error("GenerateSessionID() suffix is empty, want a KSUID value")
	}
}

func TestGenerateSessionID_Unique(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		id := GenerateSessionID()
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate session ID on iteration %d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestGetDependencyVersion_KnownDep(t *testing.T) {
	// ksuid is a direct dependency of this package, so it should be in build info.
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
