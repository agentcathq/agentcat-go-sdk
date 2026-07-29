package event

import (
	"errors"
	"strings"
	"testing"
	"time"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
)

func TestNewEventID(t *testing.T) {
	id := NewEventID()
	if !strings.HasPrefix(id, "evt_") {
		t.Errorf("expected event ID to start with 'evt_', got %s", id)
	}
	if len(id) < 5 {
		t.Errorf("expected event ID to be non-trivial, got %s", id)
	}

	// Ensure uniqueness
	id2 := NewEventID()
	if id == id2 {
		t.Error("expected unique event IDs")
	}
}

func TestNewEvent(t *testing.T) {
	projectID := "proj_123"
	sessionID := "ses_456"
	clientName := "test-client"
	serverName := "test-server"
	ipAddress := "127.0.0.1"

	session := &core.Session{
		ProjectID:  &projectID,
		SessionID:  &sessionID,
		ClientName: &clientName,
		ServerName: &serverName,
		IpAddress:  &ipAddress,
	}

	tests := []struct {
		name         string
		session      *core.Session
		eventType    string
		duration     *int32
		isError      bool
		errorDetails error
		validate     func(*testing.T, *Event)
	}{
		{
			name:      "nil session returns nil",
			session:   nil,
			eventType: "mcp:tools/call",
			validate: func(t *testing.T, evt *Event) {
				if evt != nil {
					t.Errorf("expected nil event for nil session, got %+v", evt)
				}
			},
		},
		{
			name:      "basic event creation",
			session:   session,
			eventType: "mcp:tools/call",
			validate: func(t *testing.T, evt *Event) {
				if evt == nil {
					t.Fatal("expected non-nil event")
				}
				if evt.Id == nil || !strings.HasPrefix(*evt.Id, "evt_") {
					t.Errorf("event ID should have 'evt_' prefix, got %v", evt.Id)
				}
				if evt.ProjectId != projectID {
					t.Errorf("expected ProjectID %s, got %v", projectID, evt.ProjectId)
				}
				if evt.GetSessionId() != sessionID {
					t.Errorf("expected SessionID %s, got %s", sessionID, evt.GetSessionId())
				}
				if evt.EventType == nil || *evt.EventType != "mcp:tools/call" {
					t.Errorf("expected EventType 'mcp:tools/call', got %v", evt.EventType)
				}
				if evt.Timestamp == nil {
					t.Error("timestamp should be set")
				}
			},
		},
		{
			name:         "event with error details",
			session:      session,
			eventType:    "mcp:tools/call",
			isError:      true,
			errorDetails: errors.New("tool execution failed"),
			validate: func(t *testing.T, evt *Event) {
				if evt == nil {
					t.Fatal("expected non-nil event")
				}
				if evt.IsError == nil || !*evt.IsError {
					t.Error("isError should be true")
				}
				if evt.Error == nil {
					t.Fatal("error details should be set")
				}

				// Validate structured error fields from CaptureException
				msg, ok := evt.Error["message"].(string)
				if !ok || msg != "tool execution failed" {
					t.Errorf("expected error message 'tool execution failed', got %v", evt.Error["message"])
				}
				typ, ok := evt.Error["type"].(string)
				if !ok || typ != "*errors.errorString" {
					t.Errorf("expected error type '*errors.errorString', got %v", evt.Error["type"])
				}
				plat, ok := evt.Error["platform"].(string)
				if !ok || plat != "go" {
					t.Errorf("expected platform 'go', got %v", evt.Error["platform"])
				}
				if _, ok := evt.Error["stack"].(string); !ok {
					t.Error("expected stack trace string")
				}
				if _, ok := evt.Error["frames"].([]map[string]any); !ok {
					t.Error("expected frames slice")
				}
			},
		},
		{
			name:      "event with error flag but no error details",
			session:   session,
			eventType: "mcp:tools/call",
			isError:   true,
			validate: func(t *testing.T, evt *Event) {
				if evt == nil {
					t.Fatal("expected non-nil event")
				}
				if evt.IsError == nil || !*evt.IsError {
					t.Error("isError should be true")
				}
				if evt.Error != nil {
					t.Errorf("error details should be nil when errorDetails is nil, got %v", evt.Error)
				}
			},
		},
		{
			name:      "event with duration",
			session:   session,
			eventType: "mcp:tools/call",
			duration:  func() *int32 { d := int32(250); return &d }(),
			validate: func(t *testing.T, evt *Event) {
				if evt == nil {
					t.Fatal("expected non-nil event")
				}
				if evt.Duration == nil || *evt.Duration != 250 {
					t.Errorf("expected duration 250, got %v", evt.Duration)
				}
			},
		},
		{
			name:      "session metadata copied to event",
			session:   session,
			eventType: "mcp:initialize",
			validate: func(t *testing.T, evt *Event) {
				if evt == nil {
					t.Fatal("expected non-nil event")
				}
				if evt.ClientName == nil || *evt.ClientName != clientName {
					t.Errorf("expected ClientName %s, got %v", clientName, evt.ClientName)
				}
				if evt.ServerName == nil || *evt.ServerName != serverName {
					t.Errorf("expected ServerName %s, got %v", serverName, evt.ServerName)
				}
				if evt.IpAddress == nil || *evt.IpAddress != ipAddress {
					t.Errorf("expected IpAddress %s, got %v", ipAddress, evt.IpAddress)
				}
			},
		},
		{
			name: "handles session with nil SessionID",
			session: &core.Session{
				ProjectID: &projectID,
				SessionID: nil,
			},
			eventType: "mcp:tools/call",
			validate: func(t *testing.T, evt *Event) {
				if evt == nil {
					t.Fatal("expected non-nil event")
				}
				if evt.GetSessionId() != "" {
					t.Errorf("expected empty SessionId, got %s", evt.GetSessionId())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := NewEvent(tt.session, tt.eventType, tt.duration, tt.isError, tt.errorDetails)
			tt.validate(t, evt)
		})
	}
}

func TestConvertToMap(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want any
	}{
		{
			name: "nil returns nil",
			v:    nil,
			want: nil,
		},
		{
			name: "simple struct converts to map",
			v: struct {
				Name  string
				Value int
			}{Name: "test", Value: 42},
			want: map[string]any{
				"Name":  "test",
				"Value": float64(42), // JSON unmarshaling converts numbers to float64
			},
		},
		{
			name: "slice of structs converts to slice of maps",
			v: []struct {
				ID string
			}{
				{ID: "1"},
				{ID: "2"},
			},
			want: []any{
				map[string]any{"ID": "1"},
				map[string]any{"ID": "2"},
			},
		},
		{
			name: "map passes through",
			v: map[string]any{
				"key": "value",
			},
			want: map[string]any{
				"key": "value",
			},
		},
		{
			name: "primitive types pass through",
			v:    "string value",
			want: "string value",
		},
		{
			name: "nested struct converts properly",
			v: struct {
				Outer struct {
					Inner string
				}
			}{
				Outer: struct{ Inner string }{Inner: "nested"},
			},
			want: map[string]any{
				"Outer": map[string]any{
					"Inner": "nested",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertToMap(tt.v)
			if !valuesEqual(got, tt.want) {
				t.Errorf("ConvertToMap() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCopySessionToEvent(t *testing.T) {
	tests := []struct {
		name     string
		session  *core.Session
		event    *Event
		validate func(*testing.T, *Event)
	}{
		{
			name:    "nil session does nothing",
			session: nil,
			event:   &Event{},
			validate: func(t *testing.T, evt *Event) {
				if evt.ClientName != nil {
					t.Error("event should remain unchanged with nil session")
				}
			},
		},
		{
			name:    "nil event does nothing",
			session: &core.Session{},
			event:   nil,
			validate: func(t *testing.T, evt *Event) {
				// Should not panic
			},
		},
		{
			name: "copies all session fields to event",
			session: &core.Session{
				IpAddress:            strPtr("192.168.1.1"),
				SdkLanguage:          strPtr("go"),
				AgentcatVersion:      strPtr("1.0.0"),
				ServerName:           strPtr("test-server"),
				ServerVersion:        strPtr("2.0.0"),
				ClientName:           strPtr("test-client"),
				ClientVersion:        strPtr("3.0.0"),
				IdentifyActorGivenId: strPtr("user123"),
				IdentifyActorName:    strPtr("John Doe"),
				IdentifyData: map[string]any{
					"email": "john@example.com",
				},
			},
			event: &Event{},
			validate: func(t *testing.T, evt *Event) {
				if evt.IpAddress == nil || *evt.IpAddress != "192.168.1.1" {
					t.Errorf("IpAddress not copied correctly")
				}
				if evt.SdkLanguage == nil || *evt.SdkLanguage != "go" {
					t.Errorf("SdkLanguage not copied correctly")
				}
				if evt.AgentcatVersion == nil || *evt.AgentcatVersion != "1.0.0" {
					t.Errorf("AgentcatVersion not copied correctly")
				}
				if evt.ServerName == nil || *evt.ServerName != "test-server" {
					t.Errorf("ServerName not copied correctly")
				}
				if evt.ServerVersion == nil || *evt.ServerVersion != "2.0.0" {
					t.Errorf("ServerVersion not copied correctly")
				}
				if evt.ClientName == nil || *evt.ClientName != "test-client" {
					t.Errorf("ClientName not copied correctly")
				}
				if evt.ClientVersion == nil || *evt.ClientVersion != "3.0.0" {
					t.Errorf("ClientVersion not copied correctly")
				}
				if evt.IdentifyActorGivenId == nil || *evt.IdentifyActorGivenId != "user123" {
					t.Errorf("IdentifyActorGivenId not copied correctly")
				}
				if evt.IdentifyActorName == nil || *evt.IdentifyActorName != "John Doe" {
					t.Errorf("IdentifyActorName not copied correctly")
				}
				if evt.IdentifyData == nil || evt.IdentifyData["email"] != "john@example.com" {
					t.Errorf("IdentifyData not copied correctly")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			CopySessionToEvent(tt.session, tt.event)
			tt.validate(t, tt.event)
		})
	}
}

func TestCreateIdentifyEvent(t *testing.T) {
	tests := []struct {
		name     string
		session  *core.Session
		validate func(*testing.T, *Event)
	}{
		{
			name:    "nil session returns nil",
			session: nil,
			validate: func(t *testing.T, evt *Event) {
				if evt != nil {
					t.Errorf("expected nil event for nil session, got %+v", evt)
				}
			},
		},
		{
			name: "creates identify event with required fields",
			session: &core.Session{
				ProjectID: strPtr("proj_abc"),
				SessionID: strPtr("ses_xyz"),
			},
			validate: func(t *testing.T, evt *Event) {
				if evt == nil {
					t.Fatal("expected non-nil event")
				}
				if evt.Id == nil || len(*evt.Id) == 0 {
					t.Error("event ID should be generated")
				}
				if evt.Id != nil && (*evt.Id)[:4] != "evt_" {
					t.Errorf("event ID should have 'evt_' prefix, got %s", *evt.Id)
				}
				if evt.ProjectId != "proj_abc" {
					t.Errorf("expected ProjectID 'proj_abc', got %v", evt.ProjectId)
				}
				if evt.GetSessionId() != "ses_xyz" {
					t.Errorf("expected SessionID 'ses_xyz', got %s", evt.GetSessionId())
				}
				if evt.EventType == nil || *evt.EventType != "agentcat:identify" {
					t.Errorf("expected EventType 'agentcat:identify', got %v", evt.EventType)
				}
				if evt.Timestamp == nil {
					t.Error("timestamp should be set")
				}
			},
		},
		{
			name: "copies session metadata",
			session: &core.Session{
				ProjectID:            strPtr("proj_123"),
				SessionID:            strPtr("ses_456"),
				ClientName:           strPtr("client"),
				ServerName:           strPtr("server"),
				IdentifyActorGivenId: strPtr("user1"),
				IdentifyActorName:    strPtr("Test User"),
			},
			validate: func(t *testing.T, evt *Event) {
				if evt == nil {
					t.Fatal("expected non-nil event")
				}
				if evt.ClientName == nil || *evt.ClientName != "client" {
					t.Error("ClientName should be copied from session")
				}
				if evt.ServerName == nil || *evt.ServerName != "server" {
					t.Error("ServerName should be copied from session")
				}
				if evt.IdentifyActorGivenId == nil || *evt.IdentifyActorGivenId != "user1" {
					t.Error("IdentifyActorGivenId should be copied from session")
				}
				if evt.IdentifyActorName == nil || *evt.IdentifyActorName != "Test User" {
					t.Error("IdentifyActorName should be copied from session")
				}
			},
		},
		{
			name: "handles session with nil SessionID",
			session: &core.Session{
				ProjectID: strPtr("proj_123"),
				SessionID: nil,
			},
			validate: func(t *testing.T, evt *Event) {
				if evt == nil {
					t.Fatal("expected non-nil event")
				}
				if evt.GetSessionId() != "" {
					t.Errorf("expected empty SessionId, got %s", evt.GetSessionId())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := CreateIdentifyEvent(tt.session)
			tt.validate(t, evt)
		})
	}
}

func TestPtr(t *testing.T) {
	s := "hello"
	p := core.Ptr(s)
	if p == nil || *p != "hello" {
		t.Errorf("Ptr() should return pointer to value, got %v", p)
	}

	i := 42
	ip := core.Ptr(i)
	if ip == nil || *ip != 42 {
		t.Errorf("Ptr() should return pointer to int, got %v", ip)
	}
}

// mockLogger is a simple logger implementation for testing
type mockLogger struct {
	logs []string
}

func (m *mockLogger) Infof(format string, args ...any) {
	m.logs = append(m.logs, format)
}

func TestLogEvent(t *testing.T) {
	t.Run("logs nil event", func(t *testing.T) {
		logger := &mockLogger{logs: []string{}}
		LogEvent(logger, nil, "Test Event")

		if len(logger.logs) != 1 {
			t.Fatalf("expected 1 log entry, got %d", len(logger.logs))
		}
		if logger.logs[0] != "%s: <nil event>" {
			t.Errorf("expected nil event log format, got %s", logger.logs[0])
		}
	})

	t.Run("logs complete event", func(t *testing.T) {
		logger := &mockLogger{logs: []string{}}

		eventID := "evt_123"
		eventType := "mcp:tools/call"
		projectID := "proj_abc"
		sessionID := "ses_xyz"
		duration := int32(100)
		isError := false
		timestamp := time.Now()

		evt := &Event{
			PublishEventRequest: agentcatapi.PublishEventRequest{
				Id:        &eventID,
				EventType: &eventType,
				ProjectId: projectID,
				Duration:  &duration,
				IsError:   &isError,
				Timestamp: &timestamp,
				Parameters: map[string]any{
					"name": "test_tool",
				},
				Response: map[string]any{
					"result": "success",
				},
			},
		}
		evt.SetSessionId(sessionID)

		LogEvent(logger, evt, "Test Event")

		// Verify some key log entries were created
		if len(logger.logs) == 0 {
			t.Fatal("expected log entries to be created")
		}

		// Check that the title was logged
		foundTitle := false
		for _, log := range logger.logs {
			if log == "=== %s ===" {
				foundTitle = true
				break
			}
		}
		if !foundTitle {
			t.Error("expected to find title log entry")
		}
	})
}

func TestLogEvent_NoPayloadLeak(t *testing.T) {
	logging.ResetForTesting()
	defer logging.ResetForTesting()
	defer logging.SetDiagnosticsSink(nil)

	const (
		secretParam  = "SUPER_SECRET_PARAM_VALUE"
		secretResp   = "SUPER_SECRET_RESPONSE_VALUE"
		secretIntent = "SUPER_SECRET_INTENT_TEXT"
		secretActor  = "SUPER_SECRET_ACTOR_NAME"
		secretData   = "SUPER_SECRET_IDENTIFY_DATA"
		secretIP     = "203.0.113.42"
	)

	var captured []string
	logging.SetDiagnosticsSink(func(_ logging.Level, msg string) {
		captured = append(captured, msg)
	})

	intent := secretIntent
	isErr := true
	actorName := secretActor
	evt := &Event{}
	evt.SetSessionId("ses_1")
	evt.UserIntent = &intent
	evt.IsError = &isErr
	evt.IdentifyActorName = &actorName
	evt.Parameters = map[string]any{"k": secretParam}
	evt.Response = map[string]any{"r": secretResp}
	evt.IdentifyData = map[string]any{"d": secretData}
	ipAddr := secretIP
	evt.IpAddress = &ipAddr

	LogEvent(logging.New(), evt, "Test Event")

	joined := strings.Join(captured, "\n")
	for _, s := range []string{secretParam, secretResp, secretIntent, secretActor, secretData, secretIP} {
		if strings.Contains(joined, s) {
			t.Errorf("payload value %q leaked into diagnostics:\n%s", s, joined)
		}
	}
	// Sanity: counts/presence still emitted.
	if !strings.Contains(joined, "Parameters: 1 field") {
		t.Errorf("expected parameter count, got:\n%s", joined)
	}
}

// Helper functions

func strPtr(s string) *string {
	return &s
}

func valuesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	switch va := a.(type) {
	case string:
		vb, ok := b.(string)
		return ok && va == vb
	case int:
		vb, ok := b.(int)
		return ok && va == vb
	case int32:
		vb, ok := b.(int32)
		return ok && va == vb
	case float64:
		vb, ok := b.(float64)
		return ok && va == vb
	case bool:
		vb, ok := b.(bool)
		return ok && va == vb
	case map[string]any:
		vb, ok := b.(map[string]any)
		if !ok || len(va) != len(vb) {
			return false
		}
		for k, vaVal := range va {
			vbVal, exists := vb[k]
			if !exists || !valuesEqual(vaVal, vbVal) {
				return false
			}
		}
		return true
	case []any:
		vb, ok := b.([]any)
		if !ok || len(va) != len(vb) {
			return false
		}
		for i := range va {
			if !valuesEqual(va[i], vb[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
