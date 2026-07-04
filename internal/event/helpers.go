package event

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/segmentio/ksuid"
	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
	"go.agentcat.com/sdk/internal/exceptions"
)

// NewEventID generates a new unique event ID with the MCPCat prefix.
func NewEventID() string {
	return fmt.Sprintf("%s_%s", core.PrefixEvent, ksuid.New().String())
}

// NewEvent creates an SDK-agnostic Event from session data and basic metadata.
func NewEvent(session *core.Session, eventType string, duration *int32, isError bool, errorDetails error) *Event {
	if session == nil {
		return nil
	}

	eventID := NewEventID()

	sessionID := ""
	if session.SessionID != nil {
		sessionID = *session.SessionID
	}

	projectID := ""
	if session.ProjectID != nil {
		projectID = *session.ProjectID
	}

	event := &Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Id:        &eventID,
			ProjectId: projectID,
			EventType: &eventType,
			Duration:  duration,
			Timestamp: core.Ptr(time.Now()),
		},
	}
	event.SetSessionId(sessionID)

	if isError {
		event.IsError = &isError
		if errorDetails != nil {
			event.Error = exceptions.CaptureException(errorDetails)
		}
	}

	CopySessionToEvent(session, event)

	return event
}

// ConvertToMap converts any value (including structs, slices of structs) to
// map[string]any or []any by marshaling to JSON and unmarshaling back. This
// ensures the redactor can process all fields. If conversion fails, the
// original value is returned to avoid impacting the server.
func ConvertToMap(v any) any {
	if v == nil {
		return nil
	}

	data, err := json.Marshal(v)
	if err != nil {
		return v
	}

	var result any
	if err := json.Unmarshal(data, &result); err != nil {
		return v
	}

	return result
}

// CopySessionToEvent copies session metadata fields to the event.
func CopySessionToEvent(session *core.Session, event *Event) {
	if session == nil || event == nil {
		return
	}

	// Copy all session fields to the event
	event.IpAddress = session.IpAddress
	event.SdkLanguage = session.SdkLanguage
	event.AgentcatVersion = session.AgentcatVersion
	event.ServerName = session.ServerName
	event.ServerVersion = session.ServerVersion
	event.ClientName = session.ClientName
	event.ClientVersion = session.ClientVersion
	event.IdentifyActorGivenId = session.IdentifyActorGivenId
	event.IdentifyActorName = session.IdentifyActorName
	event.IdentifyData = session.IdentifyData
}

// CreateIdentifyEvent creates an Event for mcpcat:identify event type.
func CreateIdentifyEvent(session *core.Session) *Event {
	if session == nil {
		return nil
	}

	eventID := NewEventID()

	sessionID := ""
	if session.SessionID != nil {
		sessionID = *session.SessionID
	}

	projectID := ""
	if session.ProjectID != nil {
		projectID = *session.ProjectID
	}

	eventType := "mcpcat:identify"
	event := &Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Id:        &eventID,
			ProjectId: projectID,
			EventType: &eventType,
			Timestamp: core.Ptr(time.Now()),
		},
	}
	event.SetSessionId(sessionID)

	CopySessionToEvent(session, event)

	return event
}

// LogEvent logs an event in a formatted, human-readable way for debugging.
func LogEvent(logger interface{ Infof(string, ...any) }, evt *Event, title string) {
	if evt == nil {
		logger.Infof("%s: <nil event>", title)
		return
	}

	logger.Infof("=== %s ===", title)

	// Basic event info
	if evt.Id != nil {
		logger.Infof("  Event ID: %s", *evt.Id)
	}
	if evt.EventType != nil {
		logger.Infof("  Event Type: %s", *evt.EventType)
	}
	if evt.ProjectId != "" {
		logger.Infof("  Project ID: %s", evt.ProjectId)
	}
	logger.Infof("  Session ID: %s", evt.GetSessionId())

	// Timing info
	if evt.Timestamp != nil {
		logger.Infof("  Timestamp: %s", evt.Timestamp.Format(time.RFC3339))
	}
	if evt.Duration != nil {
		logger.Infof("  Duration: %d ms", *evt.Duration)
	}

	// Error status
	if evt.IsError != nil && *evt.IsError {
		logger.Infof("  Is Error: true")
	}

	// User intent (length only — never the text)
	if evt.UserIntent != nil {
		logger.Infof("  User Intent: %d chars", len(*evt.UserIntent))
	}

	// Resource name (for resource events)
	if evt.ResourceName != nil {
		logger.Infof("  Resource Name: %s", *evt.ResourceName)
	}

	// Parameters (count only)
	if len(evt.Parameters) > 0 {
		logger.Infof("  Parameters: %d field(s)", len(evt.Parameters))
	}

	// Response (count only)
	if len(evt.Response) > 0 {
		logger.Infof("  Response: %d field(s)", len(evt.Response))
	}

	// Session metadata
	logger.Infof("  Session Metadata:")
	if evt.ClientName != nil {
		logger.Infof("    Client: %s", *evt.ClientName)
		if evt.ClientVersion != nil {
			logger.Infof("    Client Version: %s", *evt.ClientVersion)
		}
	}
	if evt.ServerName != nil {
		logger.Infof("    Server: %s", *evt.ServerName)
		if evt.ServerVersion != nil {
			logger.Infof("    Server Version: %s", *evt.ServerVersion)
		}
	}
	if evt.SdkLanguage != nil {
		logger.Infof("    SDK Language: %s", *evt.SdkLanguage)
	}
	if evt.AgentcatVersion != nil {
		logger.Infof("    AgentCat Version: %s", *evt.AgentcatVersion)
	}

	// Identity info (Actor ID kept; name value dropped; data counted)
	if evt.IdentifyActorGivenId != nil || evt.IdentifyActorName != nil {
		logger.Infof("  Identity:")
		if evt.IdentifyActorGivenId != nil {
			logger.Infof("    Actor ID: %s", *evt.IdentifyActorGivenId)
		}
		if len(evt.IdentifyData) > 0 {
			logger.Infof("    Additional Data: %d field(s)", len(evt.IdentifyData))
		}
	}

	logger.Infof("=== End %s ===", title)
}
