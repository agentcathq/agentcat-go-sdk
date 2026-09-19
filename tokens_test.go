package agentcat

import "testing"

func TestApplyTokenEstimates(t *testing.T) {
	evt := &Event{}
	evt.Parameters = map[string]any{
		"name":      "probe",
		"arguments": map[string]any{"text": "hi there"}, // 19 bytes -> 6
		"extra":     map[string]any{"headers": map[string]any{"x": "ignored"}},
	}
	evt.Response = map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": "hello world"}}, // 11 -> 4
		"structuredContent": map[string]any{"ignored": true},
		"isError":           false,
	}

	ApplyTokenEstimates(evt)

	if evt.InputTokens == nil || *evt.InputTokens != 6 {
		t.Errorf("InputTokens = %v, want 6", evt.InputTokens)
	}
	if evt.OutputTokens == nil || *evt.OutputTokens != 4 {
		t.Errorf("OutputTokens = %v, want 4", evt.OutputTokens)
	}
}

type panickyValue struct{}

func (panickyValue) MarshalJSON() ([]byte, error) { panic("marshal bug") }

func TestApplyTokenEstimatesNeverPanics(t *testing.T) {
	evt := &Event{}
	evt.Parameters = map[string]any{"arguments": map[string]any{"v": panickyValue{}}}
	evt.Response = map[string]any{"v": panickyValue{}}
	ApplyTokenEstimates(evt) // must return, not panic
	if evt.InputTokens != nil || evt.OutputTokens != nil {
		t.Errorf("a panicking payload must leave both fields nil, got %v / %v", evt.InputTokens, evt.OutputTokens)
	}
}

func TestApplyTokenEstimatesOmitsMissingSides(t *testing.T) {
	evt := &Event{}
	evt.Parameters = map[string]any{"name": "probe"} // no arguments key
	ApplyTokenEstimates(evt)
	if evt.InputTokens != nil {
		t.Errorf("InputTokens = %v, want nil", *evt.InputTokens)
	}
	if evt.OutputTokens != nil {
		t.Errorf("OutputTokens = %v, want nil", *evt.OutputTokens)
	}
	ApplyTokenEstimates(nil) // must not panic
}
