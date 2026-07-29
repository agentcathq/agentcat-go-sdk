package exporters

import (
	"time"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/logging"
)

func strPtr(s string) *string { return &s }

// testEvent builds a fully-populated event for exporter payload assertions.
func testEvent() *core.Event {
	duration := int32(150)
	isError := false
	ts := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	evt := &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Id:                   strPtr("evt_2ZyBQqANd3XrLplhrVwvNGCwt4q"),
			ProjectId:            "proj_test",
			EventType:            strPtr("mcp:tools/call"),
			ResourceName:         strPtr("add_todo"),
			Duration:             &duration,
			Timestamp:            &ts,
			IsError:              &isError,
			UserIntent:           strPtr("add a todo item"),
			IdentifyActorGivenId: strPtr("user-42"),
			IdentifyActorName:    strPtr("Ada Lovelace"),
			ClientName:           strPtr("test-client"),
			ClientVersion:        strPtr("1.2.3"),
			ServerName:           strPtr("todo-server"),
			ServerVersion:        strPtr("0.9.0"),
			Parameters:           map[string]any{"title": "buy milk"},
			Response:             map[string]any{"content": []any{map[string]any{"type": "text", "text": "done"}}},
			Tags:                 &map[string]string{"env": "staging", "region": "us-east"},
			Properties:           map[string]any{"deployment": "canary"},
		},
	}
	evt.SetSessionId("ses_2ZyBQqANd3XrLplhrVwvNGCwt4r")
	return evt
}

// testErrorEvent builds an error event with structured error details.
func testErrorEvent() *core.Event {
	evt := testEvent()
	evt.IsError = core.Ptr(true)
	evt.Error = map[string]any{
		"message": "todo not found",
		"type":    "NotFoundError",
		"stack":   "todo not found\n\tat handler",
	}
	return evt
}

// testLogger returns the shared logger (debug off, output discarded).
func testLogger() *logging.Logger {
	return logging.New()
}
