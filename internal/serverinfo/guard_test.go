package serverinfo

import (
	"fmt"
	"testing"
	"time"
)

func newMemory() *memory { return &memory{attempts: map[attemptKey]time.Time{}} }

var t0 = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func TestGuard_V7_OneAttemptPerHourPerKey(t *testing.T) {
	m := newMemory()
	if !m.check(TriggerToolsList, "proj_a", "demo", true, t0) {
		t.Fatal("first check: want due")
	}
	for i := 0; i < 2; i++ {
		if m.check(TriggerToolsList, "proj_a", "demo", true, t0) {
			t.Fatal("repeat check at the same moment: want not due")
		}
	}
	if m.check(TriggerToolsList, "proj_a", "demo", true, t0.Add(3599*time.Second)) {
		t.Fatal("check at 3,599 s: want not due")
	}
	if !m.check(TriggerToolsList, "proj_a", "demo", true, t0.Add(3600*time.Second)) {
		t.Fatal("check at 3,600 s: want due")
	}
}

func TestGuard_V8_KeyIsProjectAndServerName(t *testing.T) {
	m := newMemory()
	if !m.check(TriggerToolsList, "proj_a", "demo", true, t0) {
		t.Fatal("first server: want due")
	}
	if m.check(TriggerToolsList, "proj_a", "demo", true, t0) {
		t.Fatal("same project and name: want not due")
	}
	if !m.check(TriggerToolsList, "proj_a", "other", true, t0) {
		t.Fatal("different name: want due")
	}
	if !m.check(TriggerToolsList, "proj_b", "demo", true, t0) {
		t.Fatal("different project: want due")
	}
}

func TestGuard_V9_NoProjectOrTracingOff(t *testing.T) {
	m := newMemory()
	if m.check(TriggerToolsList, "", "demo", true, t0) {
		t.Fatal("empty project ID: want not due")
	}
	if m.check(TriggerToolsList, "proj_a", "demo", false, t0) {
		t.Fatal("tracing off: want not due")
	}
	if len(m.attempts) != 0 {
		t.Fatalf("attempts recorded for skipped checks: %v", m.attempts)
	}
}

func TestGuard_V19_ToolCallNeedsMemoryAgeOf60Seconds(t *testing.T) {
	m := newMemory()
	if m.check(TriggerToolCall, "proj_a", "demo", true, t0) {
		t.Fatal("tool call at age 0 s: want not due")
	}
	if m.check(TriggerToolCall, "proj_a", "demo", true, t0.Add(59*time.Second)) {
		t.Fatal("tool call at age 59 s: want not due")
	}
	if !m.check(TriggerToolCall, "proj_a", "demo", true, t0.Add(60*time.Second)) {
		t.Fatal("tool call at age 60 s: want due")
	}
}

func TestGuard_V17_NewMemorySendsForListAndFeedbackOnly(t *testing.T) {
	m := newMemory()
	if m.check(TriggerToolCall, "proj_a", "demo", true, t0) {
		t.Fatal("tool call in a new memory: want not due")
	}
	if !m.check(TriggerToolsList, "proj_a", "demo", true, t0) {
		t.Fatal("tools/list in a new memory: want due")
	}

	m = newMemory()
	if !m.check(TriggerFeedback, "proj_a", "demo", true, t0) {
		t.Fatal("get_more_tools in a new memory: want due")
	}
}

func TestGuard_AgeCountsFromTheFirstCheckOfAnyKind(t *testing.T) {
	m := newMemory()
	m.check(TriggerToolsList, "", "demo", true, t0) // skipped, but it starts the memory
	if !m.check(TriggerToolCall, "proj_a", "demo", true, t0.Add(60*time.Second)) {
		t.Fatal("tool call 60 s after the first check: want due")
	}
}

func TestGuard_EmptiesTheMapWhenFull(t *testing.T) {
	m := newMemory()
	for i := 0; i < maxKeys; i++ {
		if !m.check(TriggerToolsList, "proj_a", fmt.Sprintf("server-%d", i), true, t0) {
			t.Fatalf("key %d: want due", i)
		}
	}
	if len(m.attempts) != maxKeys {
		t.Fatalf("attempts = %d, want %d", len(m.attempts), maxKeys)
	}
	if !m.check(TriggerToolsList, "proj_a", "one-more", true, t0) {
		t.Fatal("key past the limit: want due")
	}
	if len(m.attempts) != 1 {
		t.Fatalf("attempts after overflow = %d, want 1", len(m.attempts))
	}
}

func TestGuard_V18_ResetMemoryStartsOver(t *testing.T) {
	restore := SetClock(func() time.Time { return t0 })
	defer restore()
	ResetMemory()
	defer ResetMemory()

	for i := 0; i < 3; i++ {
		ResetMemory()
		if Check(TriggerToolCall, "proj_a", "demo", true) {
			t.Fatalf("tool call %d in an empty memory: want not due", i)
		}
		ResetMemory()
		if !Check(TriggerToolsList, "proj_a", "demo", true) {
			t.Fatalf("tools/list %d in an empty memory: want due", i)
		}
	}
}
