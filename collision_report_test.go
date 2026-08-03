package agentcat

import (
	"strings"
	"sync"
	"testing"

	"go.agentcat.com/sdk/v2/internal/logging"
)

// captureLogs collects every entry emitted while fn runs. The sink is a
// package-level singleton, so this must not run in parallel with other
// log-asserting tests.
type logEntry struct {
	level logging.Level
	msg   string
}

func captureLogs(t *testing.T, fn func()) []logEntry {
	t.Helper()
	var mu sync.Mutex
	var got []logEntry
	logging.SetDiagnosticsSink(func(level logging.Level, msg string) {
		mu.Lock()
		got = append(got, logEntry{level, msg})
		mu.Unlock()
	})
	t.Cleanup(func() { logging.SetDiagnosticsSink(nil) })
	fn()
	logging.SetDiagnosticsSink(nil)
	mu.Lock()
	defer mu.Unlock()
	return got
}

func collisionLines(logs []logEntry, tool string) []logEntry {
	var out []logEntry
	for _, l := range logs {
		if strings.Contains(l.msg, `"`+tool+`"`) && strings.Contains(l.msg, "already declares") {
			out = append(out, l)
		}
	}
	return out
}

// TestSessionCollisionIsReportedOncePerTool pins the deduplication.
// buildInjectedList runs on every tools/list, so an undeduped report would
// repeat for the life of the process — which is how customers learn to ignore
// their own logs.
func TestSessionCollisionIsReportedOncePerTool(t *testing.T) {
	reg := &Registries{
		InjectedParams:      map[string][]string{"own": {"context"}, "fine": {"session_id", "context"}},
		CustomerOwnedParams: map[string][]string{"own": {"session_id"}},
	}
	instance := &AgentCatInstance{ProjectID: "proj_test"}

	logs := captureLogs(t, func() {
		for range 3 {
			ReportSessionParamCollisions(instance, reg)
		}
	})

	lines := collisionLines(logs, "own")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 report across 3 lists, got %d: %v", len(lines), lines)
	}
	if lines[0].level != logging.LevelError {
		t.Errorf("a session_id collision costs correlation and must be an ERROR, got level %v: %q",
			lines[0].level, lines[0].msg)
	}
	// The remediation is the point: a warning with no fix is noise.
	for _, want := range []string{"ResolveSessionID", "without a session", "still reaches your handler"} {
		if !strings.Contains(lines[0].msg, want) {
			t.Errorf("report is missing %q: %q", want, lines[0].msg)
		}
	}
	// A tool AgentCat did inject into is never reported.
	if got := collisionLines(logs, "fine"); len(got) != 0 {
		t.Errorf("a non-colliding tool must not be reported: %v", got)
	}
}

func TestSessionCollisionReportsEachToolAndEachInstance(t *testing.T) {
	reg := &Registries{
		InjectedParams: map[string][]string{"a": {}, "b": {}},
		CustomerOwnedParams: map[string][]string{
			"a": {"session_id"},
			"b": {"session_id", "agent_id"},
		},
	}

	first := &AgentCatInstance{ProjectID: "proj_test"}
	logs := captureLogs(t, func() {
		ReportSessionParamCollisions(first, reg)
		ReportSessionParamCollisions(first, reg)
	})
	for _, tool := range []string{"a", "b"} {
		if got := collisionLines(logs, tool); len(got) != 1 {
			t.Errorf("tool %q reported %d times, want 1", tool, len(got))
		}
	}

	// Dedup is per instance, not global: a second tracked server — including a
	// fresh per-request factory instance — reports for itself.
	second := &AgentCatInstance{ProjectID: "proj_test"}
	logs = captureLogs(t, func() { ReportSessionParamCollisions(second, reg) })
	if got := collisionLines(logs, "a"); len(got) != 1 {
		t.Errorf("a second instance reported %d times, want 1", len(got))
	}
}

// An agent_id-only collision must not produce the session_id error: it loses
// an attribute, not the thread.
func TestAgentIDCollisionIsNotASessionError(t *testing.T) {
	reg := &Registries{
		InjectedParams:      map[string][]string{"t": {"session_id", "context"}},
		CustomerOwnedParams: map[string][]string{"t": {"agent_id"}},
	}
	logs := captureLogs(t, func() {
		ReportSessionParamCollisions(&AgentCatInstance{ProjectID: "p"}, reg)
	})
	if got := collisionLines(logs, "t"); len(got) != 0 {
		t.Errorf("an agent_id collision must not report a lost session: %v", got)
	}
}

func TestReportSessionParamCollisionsToleratesNils(t *testing.T) {
	ReportSessionParamCollisions(nil, &Registries{})
	ReportSessionParamCollisions(&AgentCatInstance{}, nil)
	ReportSessionParamCollisions(nil, nil)
}

// The dedup set is written from the list path, which can run concurrently on
// one instance (two clients listing at once).
func TestCollisionReportIsRaceFree(t *testing.T) {
	reg := &Registries{
		InjectedParams:      map[string][]string{"own": {}},
		CustomerOwnedParams: map[string][]string{"own": {"session_id"}},
	}
	instance := &AgentCatInstance{ProjectID: "p"}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ReportSessionParamCollisions(instance, reg)
		}()
	}
	wg.Wait()
}
