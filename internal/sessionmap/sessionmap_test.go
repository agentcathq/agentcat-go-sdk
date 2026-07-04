package sessionmap

import (
	"sync"
	"testing"
	"time"

	"go.agentcat.com/sdk/internal/core"
)

func newTestSession(id string) *core.Session {
	return &core.Session{SessionID: &id}
}

func newProtectedSession(id string) *ProtectedSession {
	return &ProtectedSession{Sess: newTestSession(id)}
}

func TestNew_DefaultTTL(t *testing.T) {
	m := New(0)
	defer m.Stop()

	if m.ttl != DefaultSessionTTL {
		t.Errorf("ttl = %v, want %v", m.ttl, DefaultSessionTTL)
	}
}

func TestNew_CustomTTL(t *testing.T) {
	ttl := 10 * time.Minute
	m := New(ttl)
	defer m.Stop()

	if m.ttl != ttl {
		t.Errorf("ttl = %v, want %v", m.ttl, ttl)
	}
}

func TestLoadOrStore_NewEntry(t *testing.T) {
	m := New(DefaultSessionTTL)
	defer m.Stop()

	ps := newProtectedSession("sess-1")
	got, loaded := m.LoadOrStore("raw-1", ps)

	if loaded {
		t.Error("loaded = true, want false for new entry")
	}
	if got != ps {
		t.Error("returned session should be the one we stored")
	}
	if m.Len() != 1 {
		t.Errorf("Len() = %d, want 1", m.Len())
	}
}

func TestLoadOrStore_ExistingEntry(t *testing.T) {
	m := New(DefaultSessionTTL)
	defer m.Stop()

	ps1 := newProtectedSession("sess-1")
	ps2 := newProtectedSession("sess-2")

	m.LoadOrStore("raw-1", ps1)
	got, loaded := m.LoadOrStore("raw-1", ps2)

	if !loaded {
		t.Error("loaded = false, want true for existing entry")
	}
	if got != ps1 {
		t.Error("returned session should be the original, not the new one")
	}
	if m.Len() != 1 {
		t.Errorf("Len() = %d, want 1 (should not duplicate)", m.Len())
	}
}

func TestLoadOrStore_MultipleDifferentKeys(t *testing.T) {
	m := New(DefaultSessionTTL)
	defer m.Stop()

	for i, key := range []string{"a", "b", "c"} {
		ps := newProtectedSession(key)
		_, loaded := m.LoadOrStore(key, ps)
		if loaded {
			t.Errorf("key %q: loaded = true, want false", key)
		}
		if m.Len() != i+1 {
			t.Errorf("after inserting %q: Len() = %d, want %d", key, m.Len(), i+1)
		}
	}
}

func TestProtectedSession_Touch(t *testing.T) {
	ps := newProtectedSession("sess-1")

	before := time.Now()
	ps.Touch()
	after := time.Now()

	last := ps.lastAccessTime()
	if last.Before(before) || last.After(after) {
		t.Errorf("lastAccessTime %v not in [%v, %v]", last, before, after)
	}
}

func TestProtectedSession_TouchUpdatesTime(t *testing.T) {
	ps := newProtectedSession("sess-1")

	ps.Touch()
	first := ps.lastAccessTime()

	time.Sleep(time.Millisecond)
	ps.Touch()
	second := ps.lastAccessTime()

	if !second.After(first) {
		t.Errorf("second Touch time %v should be after first %v", second, first)
	}
}

func TestLoadOrStore_TouchesExistingSession(t *testing.T) {
	m := New(DefaultSessionTTL)
	defer m.Stop()

	ps := newProtectedSession("sess-1")
	m.LoadOrStore("raw-1", ps)

	first := ps.lastAccessTime()
	time.Sleep(time.Millisecond)

	m.LoadOrStore("raw-1", newProtectedSession("unused"))
	second := ps.lastAccessTime()

	if !second.After(first) {
		t.Errorf("LoadOrStore on existing key should Touch; got %v then %v", first, second)
	}
}

func TestLoadOrStore_TouchesNewSession(t *testing.T) {
	m := New(DefaultSessionTTL)
	defer m.Stop()

	ps := newProtectedSession("sess-1")
	m.LoadOrStore("raw-1", ps)

	last := ps.lastAccessTime()
	if last.IsZero() {
		t.Error("new session should be Touch()-ed on store")
	}
}

func TestEvict(t *testing.T) {
	ttl := 50 * time.Millisecond
	m := New(ttl)
	defer m.Stop()

	ps := newProtectedSession("sess-1")
	m.LoadOrStore("raw-1", ps)

	if m.Len() != 1 {
		t.Fatalf("Len() = %d, want 1 before eviction", m.Len())
	}

	// Wait long enough for the session to expire, then trigger eviction manually.
	time.Sleep(ttl + 10*time.Millisecond)
	m.evict(time.Now())

	if m.Len() != 0 {
		t.Errorf("Len() = %d, want 0 after eviction of expired session", m.Len())
	}
}

func TestEvict_KeepsFreshSessions(t *testing.T) {
	ttl := time.Hour
	m := New(ttl)
	defer m.Stop()

	ps := newProtectedSession("fresh")
	m.LoadOrStore("raw-fresh", ps)

	m.evict(time.Now())

	if m.Len() != 1 {
		t.Error("recently-touched session should not be evicted")
	}
}

func TestEvict_MixedStaleFresh(t *testing.T) {
	ttl := 50 * time.Millisecond
	m := New(ttl)
	defer m.Stop()

	stale := newProtectedSession("stale")
	m.LoadOrStore("raw-stale", stale)

	time.Sleep(ttl + 10*time.Millisecond)

	fresh := newProtectedSession("fresh")
	m.LoadOrStore("raw-fresh", fresh)

	m.evict(time.Now())

	if m.Len() != 1 {
		t.Errorf("Len() = %d, want 1 (stale evicted, fresh kept)", m.Len())
	}

	// Verify the fresh one is still there.
	got, loaded := m.LoadOrStore("raw-fresh", newProtectedSession("unused"))
	if !loaded {
		t.Error("fresh session should still be present")
	}
	if got != fresh {
		t.Error("fresh session should be the same object")
	}

	// Verify the stale one is gone.
	_, loaded = m.LoadOrStore("raw-stale", newProtectedSession("new"))
	if loaded {
		t.Error("stale session should have been evicted")
	}
}

func TestStop_Idempotent(t *testing.T) {
	m := New(DefaultSessionTTL)

	// Calling Stop multiple times should not panic.
	m.Stop()
	m.Stop()
	m.Stop()
}

func TestLen_EmptyMap(t *testing.T) {
	m := New(DefaultSessionTTL)
	defer m.Stop()

	if m.Len() != 0 {
		t.Errorf("Len() = %d, want 0 for empty map", m.Len())
	}
}

func TestConcurrentLoadOrStore(t *testing.T) {
	m := New(DefaultSessionTTL)
	defer m.Stop()

	const goroutines = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	winners := make(chan *ProtectedSession, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			ps := newProtectedSession("concurrent")
			got, _ := m.LoadOrStore("shared-key", ps)
			winners <- got
		}()
	}
	wg.Wait()
	close(winners)

	// All goroutines should see the same ProtectedSession instance.
	var first *ProtectedSession
	for got := range winners {
		if first == nil {
			first = got
			continue
		}
		if got != first {
			t.Fatal("concurrent LoadOrStore returned different instances for same key")
		}
	}

	if m.Len() != 1 {
		t.Errorf("Len() = %d, want 1 after concurrent stores to same key", m.Len())
	}
}

func TestConcurrentLoadOrStore_DifferentKeys(t *testing.T) {
	m := New(DefaultSessionTTL)
	defer m.Stop()

	const goroutines = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	keys := make([]string, goroutines)
	for i := 0; i < goroutines; i++ {
		keys[i] = string(rune('A' + i))
	}

	for _, key := range keys {
		go func(k string) {
			defer wg.Done()
			ps := newProtectedSession(k)
			m.LoadOrStore(k, ps)
		}(key)
	}
	wg.Wait()

	if m.Len() != goroutines {
		t.Errorf("Len() = %d, want %d", m.Len(), goroutines)
	}
}

func TestConcurrentTouchAndEvict(t *testing.T) {
	ttl := 50 * time.Millisecond
	m := New(ttl)
	defer m.Stop()

	ps := newProtectedSession("sess")
	m.LoadOrStore("key", ps)

	var wg sync.WaitGroup

	// Spawn goroutines that continuously Touch.
	wg.Add(1)
	done := make(chan struct{})
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				ps.Touch()
			}
		}
	}()

	// Spawn goroutines that trigger eviction.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			m.evict(time.Now())
			time.Sleep(time.Millisecond)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	close(done)
	wg.Wait()

	// Session should still exist because Touch kept it alive.
	if m.Len() != 1 {
		t.Error("continuously-touched session should survive eviction")
	}
}

func TestProtectedSession_IdentifyOnce(t *testing.T) {
	ps := newProtectedSession("sess-1")

	callCount := 0
	for i := 0; i < 10; i++ {
		ps.IdentifyOnce.Do(func() {
			callCount++
		})
	}

	if callCount != 1 {
		t.Errorf("IdentifyOnce ran %d times, want exactly 1", callCount)
	}
}

func TestProtectedSession_MuProtectsConcurrentAccess(t *testing.T) {
	ps := newProtectedSession("sess")

	var wg sync.WaitGroup
	const goroutines = 50

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			ps.Mu.Lock()
			id := string(rune('A' + n))
			ps.Sess.SessionID = &id
			ps.Mu.Unlock()
		}(i)
	}
	wg.Wait()

	if ps.Sess.SessionID == nil {
		t.Error("SessionID should not be nil after concurrent writes")
	}
}
