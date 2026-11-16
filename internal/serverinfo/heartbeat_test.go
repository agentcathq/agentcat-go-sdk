package serverinfo

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// No test of this package reaches the network unless it installs a
	// sender of its own.
	SetSender(func(context.Context, *Request) (int, error) { return 202, nil })
	os.Exit(m.Run())
}

func TestStart_SendsOneBodyOffTheCallingGoroutine(t *testing.T) {
	rec := useRecorder(t)
	sawHeartbeatContext := make(chan bool, 1)
	src := &Source{
		ServerVersion: "1.2.3",
		Tools: func(ctx context.Context, _ string) (Page, error) {
			sawHeartbeatContext <- IsHeartbeat(ctx)
			return Page{Items: []any{item("name", "alpha")}}, nil
		},
	}
	reqCtx, cancel := context.WithCancel(context.Background())
	Start(reqCtx, demoTarget, func() (*Source, error) { return src, nil })
	cancel() // the end of the request does not cancel the heartbeat

	if !Wait(5 * time.Second) {
		t.Fatal("heartbeat did not finish")
	}
	if !<-sawHeartbeatContext {
		t.Error("the list handler did not get the heartbeat's context")
	}
	bodies := rec.got()
	if len(bodies) != 1 {
		t.Fatalf("POSTs = %d, want 1", len(bodies))
	}
	if bodies[0].GetServerVersion() != "1.2.3" || bodies[0].GetServerName() != "demo-server" {
		t.Errorf("body = %+v", bodies[0])
	}
}

func TestStart_PanicInLoadSendsNothingAndDoesNotCrash(t *testing.T) {
	rec := useRecorder(t)
	Start(context.Background(), demoTarget, func() (*Source, error) { panic("load exploded") })
	if !Wait(5 * time.Second) {
		t.Fatal("heartbeat did not finish")
	}
	if got := len(rec.got()); got != 0 {
		t.Fatalf("POSTs = %d, want 0", got)
	}
}

func TestStart_BuildPastItsTimeLimitSendsNothing(t *testing.T) {
	rec := useRecorder(t)
	prev := buildTimeout
	buildTimeout = 50 * time.Millisecond
	defer func() { buildTimeout = prev }()

	release := make(chan struct{})
	defer close(release)
	target := demoTarget
	target.Redact = func(s string) string {
		<-release
		return s
	}
	src := &Source{Instructions: "x", Tools: pages(10, item("name", "alpha"))}
	Start(context.Background(), target, func() (*Source, error) { return src, nil })
	if !Wait(5 * time.Second) {
		t.Fatal("heartbeat did not stop at its time limit")
	}
	if got := len(rec.got()); got != 0 {
		t.Fatalf("POSTs = %d, want 0", got)
	}
}

func TestIsHeartbeat_PlainContextIsNot(t *testing.T) {
	if IsHeartbeat(context.Background()) {
		t.Fatal("a plain context counts as a heartbeat")
	}
}
