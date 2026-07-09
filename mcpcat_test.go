package agentcat

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/publisher"
)

// testServer is a dummy type to satisfy the registry's pointer requirement.
type testServer struct{ name string }

func TestRegisterServer_And_GetInstance(t *testing.T) {
	server := &testServer{name: "test"}
	instance := &MCPcatInstance{
		ProjectID: "proj_123",
		Options:   &Options{},
		ServerRef: server,
	}

	RegisterServer(server, instance)
	defer UnregisterServer(server)

	got := GetInstance(server)
	if got == nil {
		t.Fatal("GetInstance returned nil")
	}
	if got.ProjectID != "proj_123" {
		t.Errorf("ProjectID = %q, want %q", got.ProjectID, "proj_123")
	}
}

func TestGetInstance_NotFound(t *testing.T) {
	got := GetInstance(&testServer{name: "nonexistent"})
	if got != nil {
		t.Error("expected nil for unregistered server")
	}
}

func TestUnregisterServer(t *testing.T) {
	server := &testServer{name: "unreg"}
	instance := &MCPcatInstance{ProjectID: "proj"}
	RegisterServer(server, instance)

	UnregisterServer(server)

	if got := GetInstance(server); got != nil {
		t.Error("expected nil after unregister")
	}
}

func TestInitPublisher(t *testing.T) {
	// Ensure clean state
	publisher.ShutdownGlobal(context.Background())

	publishFn := InitPublisher(nil, "", nil)
	defer Shutdown(context.Background())

	if publishFn == nil {
		t.Fatal("InitPublisher returned nil function")
	}

	// Should not panic on nil event
	publishFn(nil)

	// Should not panic on valid event
	evt := &Event{}
	publishFn(evt)
}

func TestInitPublisher_WithRedactFunc(t *testing.T) {
	publisher.ShutdownGlobal(context.Background())

	redactFn := func(s string) string { return "***" }
	publishFn := InitPublisher(redactFn, "", nil)
	defer Shutdown(context.Background())

	if publishFn == nil {
		t.Fatal("InitPublisher returned nil function")
	}
}

func TestShutdown(t *testing.T) {
	publisher.ShutdownGlobal(context.Background())

	_ = InitPublisher(nil, "", nil)

	err := Shutdown(context.Background())
	if err != nil {
		t.Errorf("Shutdown returned error: %v", err)
	}
}

func TestShutdown_NoPublisher(t *testing.T) {
	publisher.ShutdownGlobal(context.Background())

	err := Shutdown(context.Background())
	if err != nil {
		t.Errorf("Shutdown with no publisher returned error: %v", err)
	}
}

func TestSetDebug(t *testing.T) {
	// Should not panic
	SetDebug(true)
	SetDebug(false)
}

func TestNewEvent(t *testing.T) {
	sess := &Session{
		ProjectID: core.Ptr("proj_123"),
		SessionID: core.Ptr("ses_abc"),
	}
	duration := int32(100)
	evt := NewEvent(sess, "tool_call", &duration, false, nil)

	if evt == nil {
		t.Fatal("NewEvent returned nil")
	}
}

func TestNewEvent_WithError(t *testing.T) {
	sess := &Session{
		ProjectID: core.Ptr("proj_123"),
	}
	evt := NewEvent(sess, "tool_call", nil, true, context.DeadlineExceeded)

	if evt == nil {
		t.Fatal("NewEvent returned nil")
	}
}

func TestNewSessionID(t *testing.T) {
	id := NewSessionID()

	prefix := string(PrefixSession) + "_"
	if !strings.HasPrefix(id, prefix) {
		t.Errorf("NewSessionID() = %q, want prefix %q", id, prefix)
	}

	// Uniqueness
	id2 := NewSessionID()
	if id == id2 {
		t.Error("two calls to NewSessionID returned the same value")
	}
}

func TestNewEventID(t *testing.T) {
	id := NewEventID()

	prefix := string(PrefixEvent) + "_"
	if !strings.HasPrefix(id, prefix) {
		t.Errorf("NewEventID() = %q, want prefix %q", id, prefix)
	}

	id2 := NewEventID()
	if id == id2 {
		t.Error("two calls to NewEventID returned the same value")
	}
}

func TestGetDependencyVersion(t *testing.T) {
	// Known dependency
	ver := GetDependencyVersion("github.com/segmentio/ksuid")
	if ver == "" {
		t.Error("expected non-empty version for known dependency")
	}

	// Unknown dependency should return "dev"
	ver = GetDependencyVersion("github.com/nonexistent/package")
	if ver != "dev" {
		t.Errorf("expected \"dev\" for unknown dep, got %q", ver)
	}
}

func TestCreateIdentifyEvent(t *testing.T) {
	sess := &Session{
		ProjectID:            core.Ptr("proj_123"),
		SessionID:            core.Ptr("ses_abc"),
		IdentifyActorGivenId: core.Ptr("user_1"),
		IdentifyActorName:    core.Ptr("Test User"),
	}

	evt := CreateIdentifyEvent(sess)
	if evt == nil {
		t.Fatal("CreateIdentifyEvent returned nil")
	}
}

func TestRedactEvent(t *testing.T) {
	evt := &Event{}
	evt.Parameters = map[string]any{"key": "value"}

	redactFn := func(s string) string {
		if s == "value" {
			return "***"
		}
		return s
	}

	err := RedactEvent(evt, redactFn)
	if err != nil {
		t.Errorf("RedactEvent returned error: %v", err)
	}
}

func TestRedactEvent_NilFunc(t *testing.T) {
	evt := &Event{}
	err := RedactEvent(evt, nil)
	if err != nil {
		t.Errorf("RedactEvent with nil func returned error: %v", err)
	}
}

func TestConvertToMap(t *testing.T) {
	type testStruct struct {
		Name string `json:"name"`
		Val  int    `json:"val"`
	}

	result := ConvertToMap(testStruct{Name: "test", Val: 42})
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if m["name"] != "test" {
		t.Errorf("name = %v, want \"test\"", m["name"])
	}
}

func TestConvertToMap_Nil(t *testing.T) {
	result := ConvertToMap(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}
}

func TestPtr(t *testing.T) {
	s := Ptr("hello")
	if *s != "hello" {
		t.Errorf("Ptr(\"hello\") = %q, want \"hello\"", *s)
	}

	n := Ptr(42)
	if *n != 42 {
		t.Errorf("Ptr(42) = %d, want 42", *n)
	}

	b := Ptr(true)
	if *b != true {
		t.Errorf("Ptr(true) = %v, want true", *b)
	}
}

func TestSentinelErrors(t *testing.T) {
	if ErrNilServer == nil {
		t.Error("ErrNilServer should not be nil")
	}
	if ErrEmptyProjectID == nil {
		t.Error("ErrEmptyProjectID should not be nil")
	}
	if !strings.Contains(ErrNilServer.Error(), "server") {
		t.Error("ErrNilServer message should mention server")
	}
	if !strings.Contains(ErrEmptyProjectID.Error(), "projectID") {
		t.Error("ErrEmptyProjectID message should mention projectID")
	}
}

func TestResolveAPIBaseURL(t *testing.T) {
	t.Run("option takes precedence over env vars", func(t *testing.T) {
		t.Setenv("AGENTCAT_API_URL", "https://agentcat-env.example.com")
		t.Setenv("MCPCAT_API_URL", "https://env.example.com")
		got := ResolveAPIBaseURL("https://option.example.com")
		if got != "https://option.example.com" {
			t.Errorf("ResolveAPIBaseURL() = %q, want %q", got, "https://option.example.com")
		}
	})

	t.Run("AGENTCAT_API_URL takes precedence over legacy MCPCAT_API_URL", func(t *testing.T) {
		t.Setenv("AGENTCAT_API_URL", "https://agentcat-env.example.com")
		t.Setenv("MCPCAT_API_URL", "https://env.example.com")
		got := ResolveAPIBaseURL("")
		if got != "https://agentcat-env.example.com" {
			t.Errorf("ResolveAPIBaseURL() = %q, want %q", got, "https://agentcat-env.example.com")
		}
	})

	t.Run("legacy MCPCAT_API_URL used when AGENTCAT_API_URL is empty", func(t *testing.T) {
		t.Setenv("AGENTCAT_API_URL", "")
		t.Setenv("MCPCAT_API_URL", "https://env.example.com")
		got := ResolveAPIBaseURL("")
		if got != "https://env.example.com" {
			t.Errorf("ResolveAPIBaseURL() = %q, want %q", got, "https://env.example.com")
		}
	})

	t.Run("returns empty when neither option nor env vars set", func(t *testing.T) {
		t.Setenv("AGENTCAT_API_URL", "")
		t.Setenv("MCPCAT_API_URL", "")
		got := ResolveAPIBaseURL("")
		if got != "" {
			t.Errorf("ResolveAPIBaseURL() = %q, want empty string", got)
		}
	})
}

func TestDefaultOptions_Wrapper(t *testing.T) {
	opts := DefaultOptions()

	if opts.DisableReportMissing {
		t.Error("expected DisableReportMissing false")
	}
	if opts.DisableToolCallContext {
		t.Error("expected DisableToolCallContext false")
	}
	if opts.Debug {
		t.Error("expected Debug false")
	}
}

func TestNewSessionMap_Wrapper(t *testing.T) {
	m := NewSessionMap(0)
	defer m.Stop()

	if m == nil {
		t.Fatal("NewSessionMap returned nil")
	}
}

func TestNewSessionMap_CustomTTL(t *testing.T) {
	ttl := 5 * time.Minute
	m := NewSessionMap(ttl)
	defer m.Stop()

	if m == nil {
		t.Fatal("NewSessionMap returned nil")
	}
}
