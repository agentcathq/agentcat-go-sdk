package event

import (
	"strings"
	"testing"
	"time"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/v2/internal/core"
	"go.agentcat.com/sdk/v2/internal/logging"
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
