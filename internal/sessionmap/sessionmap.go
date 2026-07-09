// Package sessionmap provides a TTL-based session store that both adapters use.
package sessionmap

import (
	"sync"
	"sync/atomic"
	"time"

	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/event"
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

	// Identity is the previous merged identity for this session, kept so
	// UserData merges across identify calls. Guarded by Mu.
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

// ApplyIdentity merges id into the session's identity under the session lock
// (UserID and UserName are overwritten; UserData is merged), stamps the
// session's identify fields, and returns the merged identity plus a new
// agentcat:identify event. The lock is released via defer so a panic can
// never leave the session mutex held; callers publish the returned event
// outside the lock. A nil id returns (nil, nil).
func (ps *ProtectedSession) ApplyIdentity(id *core.UserIdentity) (*core.UserIdentity, *core.Event) {
	if id == nil {
		return nil, nil
	}

	ps.Mu.Lock()
	defer ps.Mu.Unlock()

	merged := core.MergeIdentities(ps.Identity, id)
	ps.Identity = merged

	ps.Sess.IdentifyActorGivenId = &merged.UserID
	ps.Sess.IdentifyActorName = &merged.UserName
	ps.Sess.IdentifyData = merged.UserData

	return merged, event.CreateIdentifyEvent(ps.Sess)
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

// Load returns the existing session for rawSessionID, or (nil, false) when no
// entry exists. A returned session is automatically Touch()-ed.
func (m *SessionMap) Load(rawSessionID string) (*ProtectedSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if existing, ok := m.sessions[rawSessionID]; ok {
		existing.Touch()
		return existing, true
	}
	return nil, false
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
