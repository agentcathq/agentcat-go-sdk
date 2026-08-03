package core

import (
	"reflect"
	"testing"
)

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()

	if opts.DisableReportMissing {
		t.Error("Expected DisableReportMissing to be false by default")
	}

	if opts.DisableToolCallContext {
		t.Error("Expected DisableToolCallContext to be false by default")
	}

	if opts.Debug {
		t.Error("Expected Debug to be false by default")
	}

	if opts.RedactSensitiveInformation != nil {
		t.Error("Expected RedactSensitiveInformation to be nil by default")
	}

	if opts.Exporters != nil {
		t.Error("Expected Exporters to be nil by default")
	}
}

func TestIDPrefixConstants(t *testing.T) {
	if PrefixSession != "ses" {
		t.Errorf("Expected PrefixSession to be 'ses', got '%s'", PrefixSession)
	}

	if PrefixEvent != "evt" {
		t.Errorf("Expected PrefixEvent to be 'evt', got '%s'", PrefixEvent)
	}
}

func TestUserIdentity(t *testing.T) {
	// Test that UserIdentity can be created and populated correctly
	identity := &UserIdentity{
		UserID:   "user123",
		UserName: "Test User",
		UserData: map[string]any{
			"email": "test@example.com",
			"role":  "admin",
		},
	}

	if identity.UserID != "user123" {
		t.Errorf("Expected UserID 'user123', got '%s'", identity.UserID)
	}

	if identity.UserName != "Test User" {
		t.Errorf("Expected UserName 'Test User', got '%s'", identity.UserName)
	}

	if len(identity.UserData) != 2 {
		t.Errorf("Expected 2 items in UserData, got %d", len(identity.UserData))
	}

	if identity.UserData["email"] != "test@example.com" {
		t.Errorf("Expected email 'test@example.com', got '%v'", identity.UserData["email"])
	}
}

func TestExporterConfig(t *testing.T) {
	// Test that ExporterConfig can be created and populated correctly
	config := &ExporterConfig{
		Type:     "otlp",
		Endpoint: "http://localhost:4318",
		Headers:  map[string]string{"Authorization": "Bearer token"},
	}

	if config.Type != "otlp" {
		t.Errorf("Expected Type 'otlp', got '%s'", config.Type)
	}

	if config.Endpoint != "http://localhost:4318" {
		t.Errorf("Expected Endpoint 'http://localhost:4318', got '%s'", config.Endpoint)
	}

	if len(config.Headers) != 1 {
		t.Errorf("Expected 1 header, got %d", len(config.Headers))
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

func TestMCPcatInstance(t *testing.T) {
	// Test that MCPcatInstance can be created
	opts := DefaultOptions()
	instance := &MCPcatInstance{
		ProjectID: "proj_test",
		Options:   &opts,
	}

	if instance.ProjectID != "proj_test" {
		t.Errorf("Expected ProjectID 'proj_test', got '%s'", instance.ProjectID)
	}

	if instance.Options == nil {
		t.Error("Expected Options to be non-nil")
	}
}

// TestAgentCatInstanceHoldsNoServerReference pins the registry's weak-lifecycle
// contract at the type level: no field on the instance may hold the server, or
// the package-level registry map would keep it alive and runtime.AddCleanup
// would never release the entry. The v1 ServerRef field did exactly that.
func TestAgentCatInstanceHoldsNoServerReference(t *testing.T) {
	typ := reflect.TypeOf(AgentCatInstance{})
	for i := range typ.NumField() {
		switch name := typ.Field(i).Name; name {
		case "ProjectID", "Options", "Registries", "RebuildTools":
			// Known-safe: none of these can reach the server strongly
			// (RebuildTools closes over it weakly).
		case "collisionsMu", "reportedCollisions":
			// Known-safe: a mutex and a set of TOOL NAME strings. Strings
			// cannot reach the server, so the cleanup still fires.
		default:
			t.Errorf("new AgentCatInstance field %q: it must not hold a strong "+
				"reference to the server — see the type's doc comment", name)
		}
	}
}

func TestOptions_DisableDiagnosticsField(t *testing.T) {
	o := Options{DisableDiagnostics: true}
	if !o.DisableDiagnostics {
		t.Fatal("DisableDiagnostics field must exist and be settable")
	}
}
