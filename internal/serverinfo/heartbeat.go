package serverinfo

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"go.agentcat.com/sdk/v2/internal/logging"
)

// Target names the server a heartbeat describes and the redaction function of
// the Track call that tracks it.
type Target struct {
	ProjectID  string
	ServerName string
	Redact     func(string) string
}

// buildTimeout bounds the list calls and the redaction of one heartbeat
// together. A build that takes longer sends nothing.
var buildTimeout = 30 * time.Second

type heartbeatKey struct{}

// IsHeartbeat reports whether ctx belongs to a heartbeat's own list calls, so
// a list request the heartbeat makes never starts another heartbeat.
func IsHeartbeat(ctx context.Context) bool {
	marked, _ := ctx.Value(heartbeatKey{}).(bool)
	return marked
}

var inflight atomic.Int64

// Start runs one heartbeat on a new goroutine and returns at once. ctx is the
// context of the request that ran the check: the list handlers get its values,
// but the end of that request does not cancel the heartbeat. load reads what
// the adapter knows about the server; it runs on the heartbeat's goroutine.
func Start(ctx context.Context, target Target, load func() (*Source, error)) {
	ctx = context.WithValue(context.WithoutCancel(ctx), heartbeatKey{}, true)
	inflight.Add(1)
	goGuarded(nil, func() {
		defer inflight.Add(-1)
		body, err := buildWithin(ctx, target, load)
		if err != nil {
			logDebug("server info: heartbeat for %q skipped: %v", target.ServerName, err)
			return
		}
		sendBody(ctx, body)
	})
}

// Wait waits until no heartbeat is running, or until timeout. It reports
// whether every heartbeat finished. For tests.
func Wait(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for inflight.Load() > 0 {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return true
}

func buildWithin(ctx context.Context, target Target, load func() (*Source, error)) (*Request, error) {
	ctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	type outcome struct {
		body *Request
		err  error
	}
	done := make(chan outcome, 1)
	goGuarded(func(recovered any) {
		done <- outcome{err: fmt.Errorf("build panicked: %v", recovered)}
	}, func() {
		src, err := load()
		if err == nil && src == nil {
			err = errors.New("no server to describe")
		}
		if err != nil {
			done <- outcome{err: err}
			return
		}
		body, err := buildBody(ctx, target, src)
		done <- outcome{body: body, err: err}
	})

	select {
	case out := <-done:
		return out.body, out.err
	case <-ctx.Done():
		return nil, fmt.Errorf("build stopped after %s", buildTimeout)
	}
}

// goGuarded runs fn on a new goroutine that recovers any panic, since a panic
// that nothing catches on a goroutine ends the process. onPanic, when set,
// learns of the panic; it must not block or panic itself.
func goGuarded(onPanic func(recovered any), fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logDebug("server info: recovered panic: %v", r)
				if onPanic != nil {
					onPanic(r)
				}
			}
		}()
		fn()
	}()
}

func logDebug(format string, args ...any) {
	logging.New().Debugf(format, args...)
}
