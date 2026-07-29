package diagnostics

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.agentcat.com/sdk/internal/logging"
)

func TestExport_PostsOTLPWithAuth(t *testing.T) {
	type captured struct {
		path string
		auth string
		body []byte
	}
	ch := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- captured{path: r.URL.Path, auth: r.Header.Get("Authorization"), body: b}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("DIAGNOSTICS_ENDPOINT", srv.URL)
	// Force-enable: the SDK auto-disables under go test; this test asserts export.
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	ResetForTest()
	defer ResetForTest()

	Init("proj_1", false, "officialsdk", "github.com/modelcontextprotocol/go-sdk")
	capture(logging.LevelInfo, "AgentCat setup started | project proj_1")
	Flush()

	got := <-ch

	if !strings.HasSuffix(got.path, "/v1/logs") {
		t.Errorf("path = %q, want suffix /v1/logs", got.path)
	}
	if !strings.HasPrefix(got.auth, "Bearer dgk_sdk_diag_") {
		t.Errorf("auth = %q, want Bearer dgk_sdk_diag_...", got.auth)
	}

	var payload otlpPayload
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatalf("body is not valid OTLP JSON: %v\n%s", err, got.body)
	}
	if len(payload.ResourceLogs) != 1 {
		t.Fatalf("resourceLogs = %d, want 1", len(payload.ResourceLogs))
	}
	rl := payload.ResourceLogs[0]
	if len(rl.ScopeLogs) != 1 || rl.ScopeLogs[0].Scope.Name != DiagnosticsScopeName {
		t.Fatalf("scope = %+v, want name %q", rl.ScopeLogs, DiagnosticsScopeName)
	}
	recs := rl.ScopeLogs[0].LogRecords
	if len(recs) == 0 || recs[0].Body.StringValue != "AgentCat setup started | project proj_1" {
		t.Fatalf("logRecords = %+v, want body match", recs)
	}
	var hasProject bool
	for _, a := range rl.Resource.Attributes {
		if a.Key == "agentcat.project_id" && a.Value.StringValue == "proj_1" {
			hasProject = true
		}
	}
	if !hasProject {
		t.Error("resource attributes must include agentcat.project_id=proj_1")
	}
}

func TestExport_TokenOverride(t *testing.T) {
	ch := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		ch <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("DIAGNOSTICS_ENDPOINT", srv.URL)
	t.Setenv("DIAGNOSTICS_TOKEN", "custom-token-123")
	// Force-enable: the SDK auto-disables under go test; this test asserts export.
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	ResetForTest()
	defer ResetForTest()

	Init("proj_1", false, "officialsdk", "p")
	capture(logging.LevelInfo, "line")
	Flush()

	if auth := <-ch; auth != "Bearer custom-token-123" {
		t.Errorf("auth = %q, want Bearer custom-token-123", auth)
	}
}

func TestFlush_DisabledIsNoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("disabled diagnostics must not POST")
	}))
	defer srv.Close()

	t.Setenv("DIAGNOSTICS_ENDPOINT", srv.URL)
	ResetForTest()
	defer ResetForTest()

	Init("p", true, "x", "y") // disabled
	capture(logging.LevelInfo, "line")
	Flush()
}

func TestFlush_TransportErrorSwallowed(t *testing.T) {
	// Point at a closed local port so httpClient.Do fails; Flush must swallow the
	// error (fire-and-forget), not panic, and still drain the buffer.
	t.Setenv("DIAGNOSTICS_ENDPOINT", "http://127.0.0.1:1/v1/logs")
	// Force-enable: the SDK auto-disables under go test; this test asserts drain behavior.
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	ResetForTest()
	defer ResetForTest()

	Init("proj_1", false, "officialsdk", "github.com/modelcontextprotocol/go-sdk")
	capture(logging.LevelInfo, "AgentCat setup complete | proj_1")

	Flush() // must return without panic even though the POST fails

	if n := bufferLenForTest(); n != 0 {
		t.Fatalf("Flush must drain the buffer before the (failing) POST, got len %d", n)
	}
}
