package core

import (
	"strings"
	"testing"

	"go.agentcat.com/sdk/internal/testutil"
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

func TestSessionString_NilSession(t *testing.T) {
	var s *Session
	result := s.String()

	if result != "Session: <nil>" {
		t.Errorf("Expected 'Session: <nil>' for nil session, got: %s", result)
	}
}

func TestSessionString_EmptySession(t *testing.T) {
	s := &Session{}
	result := s.String()

	// Should contain basic structure with "<not set>" for missing values
	if !strings.Contains(result, "Session {") {
		t.Error("Expected session string to start with 'Session {'")
	}
	if !strings.Contains(result, "Client: <not set>") {
		t.Error("Expected '<not set>' for ClientName")
	}
	if !strings.Contains(result, "Server: <not set>") {
		t.Error("Expected '<not set>' for ServerName")
	}
	if !strings.Contains(result, "SDK: <not set>") {
		t.Error("Expected '<not set>' for SdkLanguage")
	}
}

func TestSessionString_FullyPopulated(t *testing.T) {
	projectID := "proj_123"
	clientName := "TestClient"
	clientVersion := "1.0.0"
	serverName := "TestServer"
	serverVersion := "2.0.0"
	sdkLanguage := "go"
	agentcatVersion := "0.1.0"
	ipAddress := "127.0.0.1"
	actorID := "user123"
	actorName := "John Doe"

	s := &Session{
		ProjectID:            &projectID,
		ClientName:           &clientName,
		ClientVersion:        &clientVersion,
		ServerName:           &serverName,
		ServerVersion:        &serverVersion,
		SdkLanguage:          &sdkLanguage,
		AgentcatVersion:      &agentcatVersion,
		IpAddress:            &ipAddress,
		IdentifyActorGivenId: &actorID,
		IdentifyActorName:    &actorName,
		IdentifyData: map[string]any{
			"role": "admin",
			"age":  30,
		},
	}

	result := s.String()

	// Check for all expected values
	expectedSubstrings := []string{
		"Session {",
		"Project: proj_123",
		"Client: TestClient v1.0.0",
		"Server: TestServer v2.0.0",
		"SDK: go (AgentCat v0.1.0)",
		"IP: 127.0.0.1",
		"Identity: ID=user123, Name=John Doe",
		"Additional Data:",
		"role=admin",
		"age=30",
	}

	for _, expected := range expectedSubstrings {
		if !strings.Contains(result, expected) {
			t.Errorf("Expected session string to contain '%s', got:\n%s", expected, result)
		}
	}
}

func TestSessionString_PartialData(t *testing.T) {
	tests := []struct {
		name        string
		session     *Session
		contains    []string
		notContains []string
	}{
		{
			name: "Only client name",
			session: &Session{
				ClientName: testutil.StrPtr("MyClient"),
			},
			contains:    []string{"Client: MyClient", "Server: <not set>"},
			notContains: []string{"v1.0.0", "IP:", "Identity:"},
		},
		{
			name: "Client with version",
			session: &Session{
				ClientName:    testutil.StrPtr("MyClient"),
				ClientVersion: testutil.StrPtr("3.0.0"),
			},
			contains:    []string{"Client: MyClient v3.0.0"},
			notContains: []string{"IP:", "Identity:"},
		},
		{
			name: "Server with version",
			session: &Session{
				ServerName:    testutil.StrPtr("MyServer"),
				ServerVersion: testutil.StrPtr("4.0.0"),
			},
			contains:    []string{"Server: MyServer v4.0.0"},
			notContains: []string{"IP:", "Identity:"},
		},
		{
			name: "Only IP address",
			session: &Session{
				IpAddress: testutil.StrPtr("192.168.1.1"),
			},
			contains:    []string{"IP: 192.168.1.1"},
			notContains: []string{"Identity:"},
		},
		{
			name: "Identity with ID only",
			session: &Session{
				IdentifyActorGivenId: testutil.StrPtr("actor456"),
			},
			contains:    []string{"Identity: ID=actor456"},
			notContains: []string{"Name=", "Additional Data:"},
		},
		{
			name: "Identity with name only",
			session: &Session{
				IdentifyActorName: testutil.StrPtr("Jane Smith"),
			},
			contains:    []string{"Identity: Name=Jane Smith"},
			notContains: []string{"ID=", "Additional Data:"},
		},
		{
			name: "Identity with both ID and name",
			session: &Session{
				IdentifyActorGivenId: testutil.StrPtr("actor789"),
				IdentifyActorName:    testutil.StrPtr("Bob Wilson"),
			},
			contains:    []string{"Identity: ID=actor789, Name=Bob Wilson"},
			notContains: []string{"Additional Data:"},
		},
		{
			name: "Identify data only",
			session: &Session{
				IdentifyData: map[string]any{
					"key": "value",
				},
			},
			contains:    []string{"Additional Data:", "key=value"},
			notContains: []string{"Identity:"},
		},
		{
			name: "SDK without version",
			session: &Session{
				SdkLanguage: testutil.StrPtr("python"),
			},
			contains:    []string{"SDK: python"},
			notContains: []string{"AgentCat v"},
		},
		{
			name: "SDK with AgentCat version",
			session: &Session{
				SdkLanguage:     testutil.StrPtr("python"),
				AgentcatVersion: testutil.StrPtr("0.2.0"),
			},
			contains: []string{"SDK: python (AgentCat v0.2.0)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.session.String()

			for _, expected := range tt.contains {
				if !strings.Contains(result, expected) {
					t.Errorf("Expected session string to contain '%s', got:\n%s", expected, result)
				}
			}

			for _, notExpected := range tt.notContains {
				if strings.Contains(result, notExpected) {
					t.Errorf("Expected session string NOT to contain '%s', got:\n%s", notExpected, result)
				}
			}
		})
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
		Type: "otlp",
		Config: map[string]any{
			"endpoint": "localhost:4317",
			"insecure": true,
		},
	}

	if config.Type != "otlp" {
		t.Errorf("Expected Type 'otlp', got '%s'", config.Type)
	}

	if len(config.Config) != 2 {
		t.Errorf("Expected 2 items in Config, got %d", len(config.Config))
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
		ServerRef: "test-server",
	}

	if instance.ProjectID != "proj_test" {
		t.Errorf("Expected ProjectID 'proj_test', got '%s'", instance.ProjectID)
	}

	if instance.Options == nil {
		t.Error("Expected Options to be non-nil")
	}

	if instance.ServerRef != "test-server" {
		t.Errorf("Expected ServerRef 'test-server', got '%v'", instance.ServerRef)
	}
}

func TestOptions_DisableDiagnosticsField(t *testing.T) {
	o := Options{DisableDiagnostics: true}
	if !o.DisableDiagnostics {
		t.Fatal("DisableDiagnostics field must exist and be settable")
	}
}
