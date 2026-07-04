package diagnostics

import (
	"os"
	"testing"
)

// TestMain defaults DIAGNOSTICS_ENDPOINT to a local blackhole so no test in this
// package can POST to the production collector. Tests that need to assert export
// behavior override it per-test via t.Setenv (which restores this baseline after).
func TestMain(m *testing.M) {
	if os.Getenv("DIAGNOSTICS_ENDPOINT") == "" {
		_ = os.Setenv("DIAGNOSTICS_ENDPOINT", "http://127.0.0.1:1/v1/logs")
	}
	os.Exit(m.Run())
}
