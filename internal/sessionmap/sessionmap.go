// Package sessionmap provides a TTL-based session store that both adapters use.
package sessionmap

import (
	"sync"
	"sync/atomic"
	"time"

	"go.agentcat.com/sdk/internal/core"
)

const (
	DefaultSessionTTL = 30 * time.Minute
	evictionInterval  = 5 * time.Minute
)

// ProtectedSession wraps a *core.Session with a mutex to protect concurrent
// access to its fields.
//
// Call Touch() on every access to keep the session alive in its SessionMap.
type ProtectedSession struct {
	Mu   sync.Mutex
	Sess *core.Session

	// Identity is the most recent merged identity for this session, used to
	// detect identity changes so identify events are only published when the
	// identity actually changes. Guarded by Mu.
	Identity *core.UserIdentity

	// lastAccessNano is updated atomically so any goroutine can call Touch
	// without holding the SessionMap lock.
	lastAccessNano atomic.Int64
}

// Touch refreshes this session's last-access time so it won't be evicted.
// Both adapters should call Touch on every request that uses this session.
func (ps *ProtectedSession) Touch() {
	ps.lastAccessNano.Store(time.Now().UnixNano())
}

func (ps *ProtectedSession) lastAccessTime() time.Time {
	return time.Unix(0, ps.lastAccessNano.Load())
}

// SessionMap is a concurrent map of raw session IDs to ProtectedSessions
// with automatic TTL-based eviction of idle sessions.
type SessionMap struct {
	mu       sync.RWMutex
	sessions map[string]*ProtectedSession
	ttl      time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
}

// New creates a SessionMap that evicts sessions idle longer than ttl.
// If ttl is 0, DefaultSessionTTL is used.
func New(ttl time.Duration) *SessionMap {
	if ttl == 0 {
		ttl = DefaultSessionTTL
	}
	m := &SessionMap{
		sessions: make(map[string]*ProtectedSession),
		ttl:      ttl,
		stopCh:   make(chan struct{}),
	}
	go m.evictionLoop()
	return m
}

// LoadOrStore returns the existing session for rawSessionID, or stores and
// returns newPS if no entry exists. The returned session is automatically
// Touch()-ed. The second return value is true if the value was loaded.
func (m *SessionMap) LoadOrStore(rawSessionID string, newPS *ProtectedSession) (*ProtectedSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.sessions[rawSessionID]; ok {
		existing.Touch()
		return existing, true
	}

	newPS.Touch()
	m.sessions[rawSessionID] = newPS
	return newPS, false
}

// Stop terminates the background eviction goroutine.
func (m *SessionMap) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
}

func (m *SessionMap) evictionLoop() {
	ticker := time.NewTicker(evictionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case now := <-ticker.C:
			m.evict(now)
		}
	}
}

func (m *SessionMap) evict(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := now.Add(-m.ttl)
	for id, ps := range m.sessions {
		if ps.lastAccessTime().Before(cutoff) {
			delete(m.sessions, id)
		}
	}
}

// Len returns the number of sessions currently stored (for testing).
func (m *SessionMap) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}
