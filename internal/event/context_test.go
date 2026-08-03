package event

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.agentcat.com/sdk/v2/internal/core"
)

func baseCtx() *EventContext {
	return &EventContext{
		ProjectID:     "proj_1",
		SessionID:     "ses_task",
		SessionSource: "minted",
		ClientName:    "claude", ClientVersion: "1.2",
		ServerName: "todo", ServerVersion: "0.1",
		SDKLanguage: "Go", AgentcatVersion: "v2.0.0",
	}
}

func TestNewToolCallEventStampsEverything(t *testing.T) {
	ec := baseCtx()
	ec.Identity = &core.UserIdentity{UserID: "u1", UserName: "Ada", UserData: map[string]any{"plan": "pro"}}
	d := int32(42)
	evt := NewToolCallEvent(ec, &d, true, errors.New("boom"))
	if evt.Id == nil || !strings.HasPrefix(*evt.Id, "evt_") {
		t.Errorf("event id = %v, want an evt_-prefixed ID", evt.Id)
	}
	if evt.Timestamp == nil {
		t.Error("timestamp must be stamped")
	}
	if evt.GetSessionId() != "ses_task" {
		t.Errorf("sessionId = %q", evt.GetSessionId())
	}
	if *evt.EventType != "mcp:tools/call" {
		t.Errorf("eventType = %q", *evt.EventType)
	}
	if *evt.ClientName != "claude" || *evt.ClientVersion != "1.2" {
		t.Error("client identity must be stamped per event")
	}
	if *evt.IdentifyActorGivenId != "u1" || *evt.IdentifyActorName != "Ada" {
		t.Error("per-call identity must be stamped")
	}
	if evt.IsError == nil || !*evt.IsError || evt.Error == nil {
		t.Error("error state must be stamped")
	}
	if *evt.Duration != 42 {
		t.Error("duration must be stamped")
	}
}

// TestNewToolCallEventErrorFlagWithoutDetails pins the branch both adapters
// take when a tool reports an error the SDK cannot turn into an error value:
// mcpgo/call_middleware.go and officialsdk/middleware.go both pass
// (isError=true, errorDetails=nil) when result.IsError is set but no error
// text can be extracted. The event must be flagged as an error while Error
// stays nil, never a fabricated blob captured from a nil error.
func TestNewToolCallEventErrorFlagWithoutDetails(t *testing.T) {
	evt := NewToolCallEvent(baseCtx(), nil, true, nil)
	if evt.IsError == nil || !*evt.IsError {
		t.Error("isError must be stamped even with no error details")
	}
	if evt.Error != nil {
		t.Errorf("Error must stay nil when errorDetails is nil, got %v", evt.Error)
	}
}

func TestApplySDKTagsMergesAfterCustomerTags(t *testing.T) {
	ec := baseCtx()
	ec.AgentID = "line1\nline2"
	ec.ProtocolVersion = "2026-07-28"
	ec.MRTR = "continuation"
	evt := NewToolCallEvent(ec, nil, false, nil)
	customer := map[string]string{"team": "billing", "agentcat_session_id_source": "spoofed"}
	evt.Tags = &customer

	ApplySDKTags(evt, ec)

	tags := *evt.Tags
	if tags["team"] != "billing" {
		t.Error("customer tags must survive")
	}
	if tags["agentcat_session_id_source"] != "minted" {
		t.Error("SDK must win tag collisions")
	}
	if tags["agentcat_agent_id"] != "line1 line2" {
		t.Errorf("agent id must be clamped, got %q", tags["agentcat_agent_id"])
	}
	if tags["agentcat_agent_id_source"] != "supplied" {
		t.Error("agent source tag missing")
	}
	if tags["agentcat_protocol_version"] != "2026-07-28" || tags["agentcat_mrtr"] != "continuation" {
		t.Error("conditional tags missing")
	}
}

func TestApplySDKTagsConditionals(t *testing.T) {
	ec := baseCtx() // no agent, no protocol, no MRTR
	evt := NewToolCallEvent(ec, nil, false, nil)
	ApplySDKTags(evt, ec)
	tags := *evt.Tags
	if tags["agentcat_session_id_source"] != "minted" {
		t.Error("session source tag is always present")
	}
	for _, k := range []string{"agentcat_agent_id", "agentcat_agent_id_source", "agentcat_protocol_version", "agentcat_mrtr"} {
		if _, ok := tags[k]; ok {
			t.Errorf("%s must be absent when not applicable", k)
		}
	}
}

// TestSessionlessEventCarriesExplicitNull pins the wire shape of an event with
// no session. Validation publishes sessionless for handles this server did not
// issue, and SetSessionId("") would mark the NullableString as SET — sending
// "session_id":"" rather than null. The TypeScript SDK sends
// `event.sessionId || null`; the empty string must never reach the API.
func TestSessionlessEventCarriesExplicitNull(t *testing.T) {
	ec := baseCtx()
	ec.SessionID = ""
	ec.SessionSource = "invalid"
	evt := NewToolCallEvent(ec, nil, false, nil)

	if evt.SessionId.Get() != nil {
		t.Errorf("sessionless event must hold a nil session, got %q", evt.GetSessionId())
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"session_id":null`) {
		t.Errorf(`marshalled event must carry "session_id":null, got %s`, raw)
	}
	if strings.Contains(string(raw), `"session_id":""`) {
		t.Errorf(`marshalled event must never carry an empty-string session: %s`, raw)
	}
}

// A session that IS present must still serialize as its value, so the null
// above is a genuine discriminator rather than a blanket change.
func TestSessionfulEventCarriesItsValue(t *testing.T) {
	evt := NewToolCallEvent(baseCtx(), nil, false, nil)
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"session_id":"`+baseCtx().SessionID+`"`) {
		t.Errorf("session must serialize as its value, got %s", raw)
	}
}
