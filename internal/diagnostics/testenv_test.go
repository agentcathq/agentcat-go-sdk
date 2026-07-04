package diagnostics

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.agentcat.com/sdk/internal/logging"
)

// TestInit_DisabledUnderGoTest_WhenEnvUnset is the core regression guard: with
// DISABLE_DIAGNOSTICS unset, a Track() call made from inside a `go test` binary
// (which is exactly the situation every consumer's suite runs in) must NOT enable
// diagnostics and must NOT send a single request to the collector. SDK-level test
// detection (testing.Testing()) protects consumers who have no TestMain guard.
func TestInit_DisabledUnderGoTest_WhenEnvUnset(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Empty string normalizes to "unset" — the default state for a consumer who
	// never sets the variable.
	t.Setenv("DISABLE_DIAGNOSTICS", "")
	t.Setenv("DIAGNOSTICS_ENDPOINT", srv.URL)
	ResetForTest()
	defer ResetForTest()

	Init("proj_x", false, "officialsdk", "github.com/modelcontextprotocol/go-sdk")

	if Enabled() {
		t.Fatal("diagnostics must be auto-disabled under go test when DISABLE_DIAGNOSTICS is unset")
	}

	capture(logging.LevelInfo, "MCPCat setup started | proj_x")
	Flush()

	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("collector received %d request(s); test runs must send zero", n)
	}
}

// TestInit_WhitespaceEnvTreatedAsUnset confirms a whitespace-only value is not a
// deliberate opt-in — it collapses to "unset", so test detection still disables.
func TestInit_WhitespaceEnvTreatedAsUnset(t *testing.T) {
	t.Setenv("DISABLE_DIAGNOSTICS", "  ")
	ResetForTest()
	defer ResetForTest()

	Init("proj_x", false, "officialsdk", "p")

	if Enabled() {
		t.Fatal("whitespace-only DISABLE_DIAGNOSTICS must behave as unset and stay disabled under go test")
	}
}

// TestInit_ForceEnabledOverridesTestDetection confirms the explicit escape hatch:
// a falsy DISABLE_DIAGNOSTICS (false/0/no/off) is a deliberate opt-in that overrides
// the go-test auto-disable.
func TestInit_ForceEnabledOverridesTestDetection(t *testing.T) {
	for _, v := range []string{"false", "0", "no", "off"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("DISABLE_DIAGNOSTICS", v)
			ResetForTest()
			defer ResetForTest()

			Init("proj_x", false, "officialsdk", "p")

			if !Enabled() {
				t.Fatalf("DISABLE_DIAGNOSTICS=%q must force-enable diagnostics even under go test", v)
			}
		})
	}
}
