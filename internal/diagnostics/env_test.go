package diagnostics

import "testing"

func TestResolveEndpoint_AlreadySuffixed(t *testing.T) {
	// An override that already ends in /v1/logs must NOT get the suffix appended twice.
	t.Setenv("DIAGNOSTICS_ENDPOINT", "https://example.com/v1/logs")
	if got := resolveEndpoint(); got != "https://example.com/v1/logs" {
		t.Errorf("resolveEndpoint() = %q, want https://example.com/v1/logs (no double suffix)", got)
	}

	// A trailing slash is trimmed first, so it still resolves to a single suffix.
	t.Setenv("DIAGNOSTICS_ENDPOINT", "https://example.com/v1/logs/")
	if got := resolveEndpoint(); got != "https://example.com/v1/logs" {
		t.Errorf("resolveEndpoint() = %q, want trailing slash trimmed to single suffix", got)
	}
}
