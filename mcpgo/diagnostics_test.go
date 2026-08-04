package mcpgo

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"go.agentcat.com/sdk/v2"
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

// TestTrack_FailedSetupWritesDebugLog pins Debug's promise on the one path
// where the customer most needs the log: a Track that fails validation must
// still write its setup-started and setup-failed lines to ~/agentcat.log.
// The setup-started line is also the only one naming the integration.
func TestTrack_FailedSetupWritesDebugLog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Cleanup(func() { agentcat.SetDebug(false) })

	s := server.NewMCPServer("test", "1.0.0")
	_, err := Track(s, "", &Options{Debug: true})
	if err != agentcat.ErrEmptyProjectID {
		t.Fatalf("err = %v, want ErrEmptyProjectID", err)
	}

	body, readErr := os.ReadFile(filepath.Join(os.Getenv("HOME"), "agentcat.log"))
	if readErr != nil {
		t.Fatalf("agentcat.log was not written despite Debug: true: %v", readErr)
	}
	for _, want := range []string{
		"AgentCat setup started",
		"integration mcpgo",
		"AgentCat setup failed",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("agentcat.log missing %q; log:\n%s", want, body)
		}
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
