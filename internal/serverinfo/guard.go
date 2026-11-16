// Package serverinfo sends the server info heartbeat: about once an hour per
// process and server name, the SDK calls the server's own list handlers and
// posts the server's name, version, title, instructions and full tool,
// resource, resource template and prompt lists to AgentCat.
//
// The request path only runs Check, which reads and writes a small map of
// times in memory. Everything else (the list calls, the redaction and the
// POST) runs on a goroutine that starts with a recover, because a panic that
// nothing catches on a goroutine ends the customer's process.
package serverinfo

import (
	"sync"
	"sync/atomic"
	"time"
)

// Trigger names the kind of request that runs a check.
type Trigger int

const (
	// TriggerToolsList is a tools/list request.
	TriggerToolsList Trigger = iota
	// TriggerFeedback is a call of the get_more_tools tool.
	TriggerFeedback
	// TriggerToolCall is any other tools/call request.
	TriggerToolCall
)

const (
	// minMemoryAge is the age a memory must reach before a plain tool call
	// can start a heartbeat. A runtime that keeps no memory between requests
	// never reaches it, so it sends only for list requests and feedback notes.
	minMemoryAge = 60 * time.Second

	// attemptInterval is the time between two attempts for one key. A failed
	// attempt waits the same time.
	attemptInterval = 3600 * time.Second

	// maxKeys bounds the attempt map; when it is full it is emptied.
	maxKeys = 1000
)

type attemptKey struct {
	projectID  string
	serverName string
}

// memory is the only state of the heartbeat: the time of the first check and
// the start time of the last attempt per key. It holds times only.
type memory struct {
	mu         sync.Mutex
	firstCheck time.Time
	attempts   map[attemptKey]time.Time
}

var (
	mem      = &memory{attempts: map[attemptKey]time.Time{}}
	clockMu  sync.RWMutex
	clockNow = time.Now
)

func now() time.Time {
	clockMu.RLock()
	defer clockMu.RUnlock()
	return clockNow()
}

// SetClock replaces the clock of the guard and returns a function that
// restores the previous one. For tests.
func SetClock(fn func() time.Time) (restore func()) {
	clockMu.Lock()
	prev := clockNow
	clockNow = fn
	clockMu.Unlock()
	return func() {
		clockMu.Lock()
		clockNow = prev
		clockMu.Unlock()
	}
}

var disabled atomic.Bool

// SetEnabled turns the heartbeat on or off for the whole process and returns
// a function that restores the previous state. For tests: a suite whose
// fixtures do not expect the heartbeat's list calls turns it off.
func SetEnabled(enabled bool) (restore func()) {
	prev := !disabled.Swap(!enabled)
	return func() { disabled.Store(!prev) }
}

// Enabled reports whether the heartbeat is on.
func Enabled() bool { return !disabled.Load() }

// ResetMemory forgets the first-check time and every attempt, as a new
// process would. For tests.
func ResetMemory() {
	mem.mu.Lock()
	mem.firstCheck = time.Time{}
	mem.attempts = map[attemptKey]time.Time{}
	mem.mu.Unlock()
}

// Check reports whether a heartbeat is due for this request, and records the
// attempt when it is, so two requests at the same moment start one heartbeat.
// It reads and writes memory only.
func Check(trigger Trigger, projectID, serverName string, tracing bool) bool {
	return mem.check(trigger, projectID, serverName, tracing, now())
}

func (m *memory) check(trigger Trigger, projectID, serverName string, tracing bool, t time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.firstCheck.IsZero() {
		m.firstCheck = t
	}
	if trigger == TriggerToolCall && t.Sub(m.firstCheck) < minMemoryAge {
		return false
	}
	if projectID == "" || !tracing {
		return false
	}

	key := attemptKey{projectID: projectID, serverName: serverName}
	last, seen := m.attempts[key]
	if seen && t.Sub(last) < attemptInterval {
		return false
	}
	if !seen && len(m.attempts) >= maxKeys {
		m.attempts = map[attemptKey]time.Time{}
	}
	m.attempts[key] = t
	return true
}
