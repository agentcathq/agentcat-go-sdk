package mcpgo

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"go.agentcat.com/sdk"
)

// TestMain is a repo-level belt-and-suspenders guard: the SDK already
// auto-disables diagnostics under go test, but we also pin DISABLE_DIAGNOSTICS=1
// package-wide so no change to the detection logic can leak traffic from our suite.
// Beacon tests opt back in per-test with DISABLE_DIAGNOSTICS=false + a local server.
func TestMain(m *testing.M) {
	_ = os.Setenv("DISABLE_DIAGNOSTICS", "1")
	os.Exit(m.Run())
}

func newDiagServer(t *testing.T) (chan string, *httptest.Server) {
	t.Helper()
	ch := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- string(b)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return ch, srv
}

func drain(ch chan string) string {
	var b strings.Builder
	for {
		select {
		case s := <-ch:
			b.WriteString(s)
			b.WriteByte('\n')
		default:
			return b.String()
		}
	}
}

func TestTrack_EmitsSetupBeacons(t *testing.T) {
	ch, srv := newDiagServer(t)
	t.Setenv("DIAGNOSTICS_ENDPOINT", srv.URL)
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	agentcat.ResetDiagnosticsForTest()
	t.Cleanup(agentcat.ResetDiagnosticsForTest)

	s := server.NewMCPServer("test", "1.0.0")
	shutdown, err := Track(s, "proj_test", &Options{})
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}

	body := drain(ch)
	if !strings.Contains(body, "AgentCat setup started") ||
		!strings.Contains(body, "AgentCat setup complete") ||
		!strings.Contains(body, "proj_test") ||
		!strings.Contains(body, "integration mcpgo") {
		t.Fatalf("beacons missing in diagnostics body:\n%s", body)
	}
}

func TestTrack_EmptyProjectIDLogsError(t *testing.T) {
	ch, srv := newDiagServer(t)
	t.Setenv("DIAGNOSTICS_ENDPOINT", srv.URL)
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	agentcat.ResetDiagnosticsForTest()
	t.Cleanup(agentcat.ResetDiagnosticsForTest)

	s := server.NewMCPServer("test", "1.0.0")
	_, err := Track(s, "", &Options{})
	if err != agentcat.ErrEmptyProjectID {
		t.Fatalf("err = %v, want ErrEmptyProjectID", err)
	}
	_ = agentcat.Shutdown(context.Background())

	body := drain(ch)
	if !strings.Contains(body, "AgentCat setup failed") {
		t.Fatalf("expected setup-failed record:\n%s", body)
	}
}
