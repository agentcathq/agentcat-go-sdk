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

func TestDeriveSessionIDFromMCPSession_Deterministic(t *testing.T) {
	a := DeriveSessionIDFromMCPSession("mcp-session-abc-123", "proj_test123")
	b := DeriveSessionIDFromMCPSession("mcp-session-abc-123", "proj_test123")
	if a != b {
		t.Errorf("same inputs produced different IDs: %q vs %q", a, b)
	}
}

func TestDeriveSessionIDFromMCPSession_DifferentInputsDiffer(t *testing.T) {
	base := DeriveSessionIDFromMCPSession("mcp-session-abc-123", "proj_test123")

	if got := DeriveSessionIDFromMCPSession("other-session", "proj_test123"); got == base {
		t.Error("different session IDs produced the same derived ID")
	}
	if got := DeriveSessionIDFromMCPSession("mcp-session-abc-123", "proj_other"); got == base {
		t.Error("different project IDs produced the same derived ID")
	}
	if got := DeriveSessionIDFromMCPSession("mcp-session-abc-123", ""); got == base {
		t.Error("empty project ID should hash differently than a set project ID")
	}
}

// TestDeriveSessionIDFromMCPSession_TypeScriptParity pins the derivation to
// vectors generated with the TypeScript SDK's deriveSessionIdFromMCPSession
// (src/modules/session.ts), so both SDKs derive identical session IDs.
func TestDeriveSessionIDFromMCPSession_TypeScriptParity(t *testing.T) {
	tests := []struct {
		mcpSessionID string
		projectID    string
		want         string
	}{
		{"mcp-session-abc-123", "proj_test123", "ses_2bnWqnQqYsZ7lqMDFWpkqB9Ebay"},
		{"mcp-session-abc-123", "", "ses_2awGVYhEGzrfGtJmPAo5Jc28EV7"},
		{"some-other-session", "proj_test123", "ses_2bcgkVQlG56y9St4NcfT7DarkO4"},
		{"user-session-12345", "proj_abc123xyz", "ses_2aTphr3eCURA3wWq3LqL1JIsNm2"},
	}

	for _, tt := range tests {
		got := DeriveSessionIDFromMCPSession(tt.mcpSessionID, tt.projectID)
		if got != tt.want {
			t.Errorf("DeriveSessionIDFromMCPSession(%q, %q) = %q, want %q",
				tt.mcpSessionID, tt.projectID, got, tt.want)
		}
	}
}
