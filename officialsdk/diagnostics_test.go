package officialsdk

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.agentcat.com/sdk"
)

func TestMain(m *testing.M) {
	// Repo-level belt-and-suspenders: the SDK already auto-disables diagnostics
	// under go test, but we also keep them off for the suite by default so unrelated
	// Track tests never emit real network traffic. Beacon tests opt back in per-test.
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

func TestTrack_EmitsSetupBeacons(t *testing.T) {
	ch, srv := newDiagServer(t)
	t.Setenv("DIAGNOSTICS_ENDPOINT", srv.URL)
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	agentcat.ResetDiagnosticsForTest()
	t.Cleanup(agentcat.ResetDiagnosticsForTest)

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	shutdown, err := Track(server, "proj_test", &Options{})
	if err != nil {
		t.Fatalf("Track returned error: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}

	body := drain(ch)
	if !strings.Contains(body, "MCPCat setup started") ||
		!strings.Contains(body, "MCPCat setup complete") ||
		!strings.Contains(body, "proj_test") ||
		!strings.Contains(body, "integration officialsdk") {
		t.Fatalf("beacons missing in diagnostics body:\n%s", body)
	}
}

func TestTrack_EmptyProjectIDLogsError(t *testing.T) {
	ch, srv := newDiagServer(t)
	t.Setenv("DIAGNOSTICS_ENDPOINT", srv.URL)
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	agentcat.ResetDiagnosticsForTest()
	t.Cleanup(agentcat.ResetDiagnosticsForTest)

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	_, err := Track(server, "", &Options{})
	if err != agentcat.ErrEmptyProjectID {
		t.Fatalf("err = %v, want ErrEmptyProjectID", err)
	}
	_ = agentcat.Shutdown(context.Background()) // flush

	body := drain(ch)
	if !strings.Contains(body, "MCPCat setup failed") {
		t.Fatalf("expected setup-failed record:\n%s", body)
	}
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
