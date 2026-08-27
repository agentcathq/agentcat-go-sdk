package inject

import (
	"testing"

	"go.agentcat.com/sdk/v2/internal/handles"
)

func TestStripUsesRegistry(t *testing.T) {
	reg := &Registries{InjectedParams: map[string][]string{
		"t":       {"session_id", "context"},
		"withown": {"context"}, // customer defines session_id themselves
	}}
	args := map[string]any{"session_id": sid("x"), "context": "why", "q": "keep"}
	got := Strip("t", args, reg)
	if _, ok := got["session_id"]; ok {
		t.Error("session_id must be stripped")
	}
	if _, ok := got["context"]; ok {
		t.Error("context must be stripped")
	}
	if got["q"] != "keep" {
		t.Error("customer args must survive")
	}
	if _, ok := args["session_id"]; !ok {
		t.Error("Strip must clone, not mutate")
	}
	// Customer-owned session_id reaches the handler untouched.
	got = Strip("withown", map[string]any{"session_id": "customer-value", "context": "c"}, reg)
	if got["session_id"] != "customer-value" {
		t.Error("customer's own session_id must not be stripped")
	}
	// Tool listed with zero injections strips nothing.
	reg.InjectedParams["empty"] = []string{}
	got = Strip("empty", map[string]any{"session_id": "kept"}, reg)
	if got["session_id"] != "kept" {
		t.Error("zero-injection tools must strip nothing")
	}
}

func TestStripFallbackWithoutRegistry(t *testing.T) {
	args := map[string]any{"session_id": "a", "agent_id": "b", "context": "c", "q": "keep"}
	got := Strip("any_tool", args, nil)
	for _, k := range []string{"session_id", "agent_id", "context"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s must be heuristically stripped", k)
		}
	}
	// get_more_tools keeps its real context parameter.
	got = Strip("get_more_tools", map[string]any{"session_id": "a", "context": "real"}, nil)
	if got["context"] != "real" {
		t.Error("get_more_tools context must survive the fallback strip")
	}
	if _, ok := got["session_id"]; ok {
		t.Error("get_more_tools handles must still be stripped")
	}
}

func TestBuildHandleMirror(t *testing.T) {
	id := sid("A")
	const agent = "opus|cc|k3n9x"

	for _, c := range []struct {
		name        string
		in          MirrorInput
		wantNil     bool
		wantSession string // "" = the session_id key must be ABSENT
		wantAgent   string // "" = the agent_id key must be ABSENT
		wantStatus  string // "" = the status key must be ABSENT
	}{
		{
			name:        "minted: announce the handle",
			in:          MirrorInput{Resolution: handles.SessionResolution{SessionID: id, Source: handles.SessionSourceMinted}},
			wantSession: id, wantStatus: "issued",
		},
		{
			name:        "supplied: active",
			in:          MirrorInput{Resolution: handles.SessionResolution{SessionID: id, Source: handles.SessionSourceSupplied}},
			wantSession: id, wantStatus: "active",
		},
		{
			name:        "supplied with agent: both handles",
			in:          MirrorInput{Resolution: handles.SessionResolution{SessionID: id, Source: handles.SessionSourceSupplied}, AgentID: agent},
			wantSession: id, wantAgent: agent, wantStatus: "active",
		},
		{
			// NO session_id key: echoing back a value we rejected would
			// confirm an ID this server never issued.
			name:       "invalid: unrecognized, name no session",
			in:         MirrorInput{Resolution: handles.SessionResolution{Source: handles.SessionSourceInvalid}},
			wantStatus: "unrecognized",
		},
		{
			name:       "invalid with agent: unrecognized, keep the agent",
			in:         MirrorInput{Resolution: handles.SessionResolution{Source: handles.SessionSourceInvalid}, AgentID: agent},
			wantAgent:  agent,
			wantStatus: "unrecognized",
		},
		{
			// The session parameter is the customer's, so no session state is
			// named — but the agent's own handle is still echoed.
			name:      "foreign with agent: agent only",
			in:        MirrorInput{Resolution: handles.SessionResolution{Source: handles.SessionSourceForeign}, AgentID: agent},
			wantAgent: agent,
		},
		{
			name:    "foreign alone: nothing to mirror",
			in:      MirrorInput{Resolution: handles.SessionResolution{Source: handles.SessionSourceForeign}},
			wantNil: true,
		},
		{
			name:      "hook with agent: agent only",
			in:        MirrorInput{Resolution: handles.SessionResolution{SessionID: id, Source: handles.SessionSourceHook, HookMode: true}, AgentID: agent},
			wantAgent: agent,
		},
		{
			name:    "hook alone: nothing to mirror",
			in:      MirrorInput{Resolution: handles.SessionResolution{SessionID: id, Source: handles.SessionSourceHook, HookMode: true}},
			wantNil: true,
		},
		{
			// The hook fallback mint is still hook mode: no parameter exists
			// for the agent to echo, so no session state is named.
			name:      "hook-mode mint with agent: agent only",
			in:        MirrorInput{Resolution: handles.SessionResolution{SessionID: id, Source: handles.SessionSourceMinted, HookMode: true}, AgentID: agent},
			wantAgent: agent,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := BuildMirror(c.in)
			if c.wantNil {
				if m != nil {
					t.Fatalf("want no mirror, got %v", m)
				}
				return
			}
			if m == nil {
				t.Fatal("want a mirror, got nil")
			}
			if got, ok := m["session_id"]; c.wantSession == "" {
				if ok {
					t.Errorf("session_id must be absent, got %v", got)
				}
			} else if got != c.wantSession {
				t.Errorf("session_id = %v, want %q", got, c.wantSession)
			}
			if got, ok := m["agent_id"]; c.wantAgent == "" {
				if ok {
					t.Errorf("agent_id must be absent, got %v", got)
				}
			} else if got != c.wantAgent {
				t.Errorf("agent_id = %v, want %q", got, c.wantAgent)
			}
			if got, ok := m["status"]; c.wantStatus == "" {
				if ok {
					t.Errorf("status must be absent, got %v", got)
				}
			} else if got != c.wantStatus {
				t.Errorf("status = %v, want %q", got, c.wantStatus)
			}
			for k := range m {
				if k != "session_id" && k != "agent_id" && k != "status" {
					t.Errorf("unexpected mirror member %q", k)
				}
			}
		})
	}
}

func TestShouldMirror(t *testing.T) {
	reg := &Registries{OutputInjected: map[string]bool{"declared": true}, InjectedParams: map[string][]string{}}
	if !ShouldMirror("declared", reg) {
		t.Error("output-registry tools mirror")
	}
	if ShouldMirror("plain", reg) {
		t.Error("tools without declared outputSchema must not mirror")
	}
	if !ShouldMirror("anything", nil) {
		t.Error("no registry at all → mirror anyway")
	}
}
