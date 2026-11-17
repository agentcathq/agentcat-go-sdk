package serverinfo

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.agentcat.com/sdk/v2/internal/logging"
)

// captureLog collects every line the SDK logs until the test ends.
func captureLog(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var lines []string
	logging.SetDiagnosticsSink(func(_ logging.Level, msg string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, msg)
	})
	t.Cleanup(func() { logging.SetDiagnosticsSink(nil) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(lines, "\n")
	}
}

func runHeartbeat(t *testing.T, target Target, src *Source) {
	t.Helper()
	Start(context.Background(), target, func() (*Source, error) { return src, nil })
	if !Wait(5 * time.Second) {
		t.Fatal("heartbeat did not finish")
	}
}

func expectNoSecret(t *testing.T, log string) {
	t.Helper()
	if log == "" {
		t.Fatal("no log line was written")
	}
	if strings.Contains(log, "secret") {
		t.Errorf("log holds customer text:\n%s", log)
	}
}

func TestLog_HandlerErrorTextIsNotLogged(t *testing.T) {
	useRecorder(t)
	log := captureLog(t)
	runHeartbeat(t, demoTarget, &Source{
		Tools:   pages(10, item("name", "alpha")),
		Prompts: failing(errors.New("secret")),
	})
	runHeartbeat(t, demoTarget, &Source{Tools: failing(errors.New("secret"))})
	expectNoSecret(t, log())
}

func TestLog_HandlerPanicValueIsNotLogged(t *testing.T) {
	useRecorder(t)
	log := captureLog(t)
	runHeartbeat(t, demoTarget, &Source{
		Tools: func(context.Context, string) (Page, error) { panic("secret") },
	})
	expectNoSecret(t, log())
}

func TestLog_RedactionPanicValueIsNotLogged(t *testing.T) {
	useRecorder(t)
	log := captureLog(t)
	target := demoTarget
	target.Redact = func(string) string { panic("secret") }
	runHeartbeat(t, target, &Source{Instructions: "x", Tools: pages(10, item("name", "alpha"))})
	expectNoSecret(t, log())
}

func TestLog_LoadPanicValueIsNotLogged(t *testing.T) {
	useRecorder(t)
	log := captureLog(t)
	Start(context.Background(), demoTarget, func() (*Source, error) { panic("secret") })
	if !Wait(5 * time.Second) {
		t.Fatal("heartbeat did not finish")
	}
	expectNoSecret(t, log())
}
