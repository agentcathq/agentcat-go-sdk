package handles

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

var errAny = errors.New("hook failed")

// Golden vectors pinned against the TypeScript SDK (src/tests/handles.test.ts).
// These MUST match across every AgentCat SDK.
func TestDeriveSessionIDGoldenVectors(t *testing.T) {
	cases := []struct{ id, project, want string }{
		{"customer-abc", "proj_1", "ses_2cOHEO0LYGADMzRvWTXXVbbgxgm"},
		{"customer-abc", "", "ses_2cZY3tvyI25O2AmL2CGVo2B1IIj"},
		{" x ", "p", "ses_2c3yR5mYKQdLaXsJNgZH6erbfQK"},
		{"x", "p", "ses_2bw285VY9apdgUgTPXKFnT6P4G0"},
		// Carried over from the retired v1 session derivation (identical
		// algorithm), pinned against the TypeScript SDK's
		// deriveSessionIdFromMCPSession. Together these also pin that a
		// different ID under the same project, and the same ID with and
		// without a project, derive to different sessions.
		{"mcp-session-abc-123", "proj_test123", "ses_2bnWqnQqYsZ7lqMDFWpkqB9Ebay"},
		{"mcp-session-abc-123", "", "ses_2awGVYhEGzrfGtJmPAo5Jc28EV7"},
		{"some-other-session", "proj_test123", "ses_2bcgkVQlG56y9St4NcfT7DarkO4"},
		{"user-session-12345", "proj_abc123xyz", "ses_2aTphr3eCURA3wWq3LqL1JIsNm2"},
	}
	for _, c := range cases {
		if got := DeriveSessionID(c.id, c.project); got != c.want {
			t.Errorf("DeriveSessionID(%q, %q) = %q, want %q", c.id, c.project, got, c.want)
		}
	}
	// Derivation does NOT trim (callers trim); " x " and "x" differ above.
	if DeriveSessionID("a", "p") != DeriveSessionID("a", "p") {
		t.Error("derivation must be stable")
	}
}

func TestMintSessionID(t *testing.T) {
	id := MintSessionID()
	if !strings.HasPrefix(id, "ses_") || len(id) != len("ses_")+27 {
		t.Errorf("MintSessionID() = %q; want ses_-prefixed 27-char KSUID", id)
	}
	if MintSessionID() == id {
		t.Error("MintSessionID must be random")
	}
}

func TestExtractHandle(t *testing.T) {
	cases := []struct {
		args map[string]any
		want string
		ok   bool
	}{
		{map[string]any{"session_id": sid("abc")}, sid("abc"), true},
		{map[string]any{"session_id": "  weird value  "}, "  weird value  ", true}, // verbatim, trim only gates emptiness
		{map[string]any{"session_id": "   "}, "", false},
		{map[string]any{"session_id": 42}, "", false},
		{map[string]any{}, "", false},
		{nil, "", false},
	}
	for _, c := range cases {
		got, ok := ExtractHandle(c.args, "session_id")
		if got != c.want || ok != c.ok {
			t.Errorf("ExtractHandle(%v) = (%q, %v), want (%q, %v)", c.args, got, ok, c.want, c.ok)
		}
	}
}

func TestIsValidSessionID(t *testing.T) {
	// Both issuing paths satisfy the predicate by construction. If either ever
	// stops doing so, every agent that echoes a handle back is silently
	// disowned, so these three are the load-bearing cases.
	for _, v := range []string{MintSessionID(), DeriveSessionID("customer-abc", "proj_1"), sid("parent")} {
		if !IsValidSessionID(v) {
			t.Errorf("IsValidSessionID(%q) = false; this SDK issued it", v)
		}
	}
	for _, c := range []struct{ name, value string }{
		{"empty", ""},
		{"prefix only", "ses_"},
		{"too short", "ses_abc"},
		{"one char short", "ses_" + strings.Repeat("a", 26)},
		{"one char long", "ses_" + strings.Repeat("a", 28)},
		{"wrong prefix", "task_" + strings.Repeat("a", 27)},
		{"no prefix", strings.Repeat("a", 27)},
		{"uppercase prefix", "SES_" + strings.Repeat("a", 27)},
		{"non-base62 body", "ses_" + strings.Repeat("-", 27)},
		{"underscore in body", "ses_" + strings.Repeat("_", 27)},
		{"inner whitespace", "ses_ " + strings.Repeat("a", 26)},
		{"leading space", " ses_" + strings.Repeat("a", 27)},
		{"trailing newline", "ses_" + strings.Repeat("a", 27) + "\n"},
		{"customer value", "my-app-session-42"},
		{"a secret", "sk_live_secret_token"},
	} {
		if IsValidSessionID(c.value) {
			t.Errorf("IsValidSessionID(%q) = true for %s; this SDK never issued it", c.value, c.name)
		}
	}
}

func TestResolveSessionHandleDecisionTable(t *testing.T) {
	valid := sid("parent")
	hookOK := func() (string, error) { return " x ", nil }

	const minted = "<minted>"
	for _, c := range []struct {
		name       string
		args       map[string]any
		hook       func() (string, error)
		ours       bool
		wantID     string
		wantSource SessionSource
	}{
		{"absent, ours: mint", map[string]any{}, nil, true, minted, SessionSourceMinted},
		// The start sentinel resolves exactly like absent: mint. Matched
		// case-insensitively and after trimming — grace for clients that do
		// not enforce the schema pattern (which documents lowercase).
		{"start sentinel, ours: mint", map[string]any{"session_id": "start"}, nil, true, minted, SessionSourceMinted},
		{"START case variant, ours: mint", map[string]any{"session_id": "START"}, nil, true, minted, SessionSourceMinted},
		{"padded Start, ours: mint", map[string]any{"session_id": "  Start "}, nil, true, minted, SessionSourceMinted},
		// The sentinel is only ours to interpret: on a customer-owned
		// parameter the literal value belongs to the customer's tool.
		{"start, not ours: sessionless", map[string]any{"session_id": "start"}, nil, false, "", SessionSourceForeign},
		{"valid, ours: trust verbatim", map[string]any{"session_id": valid}, nil, true, valid, SessionSourceSupplied},
		{"padded valid, ours: trust trimmed", map[string]any{"session_id": " " + valid + " "}, nil, true, valid, SessionSourceSupplied},
		{"garbage, ours: sessionless", map[string]any{"session_id": "nope"}, nil, true, "", SessionSourceInvalid},
		{"value, not ours: sessionless", map[string]any{"session_id": "customer-value"}, nil, false, "", SessionSourceForeign},
		{"absent, not ours: sessionless", map[string]any{}, nil, false, "", SessionSourceForeign},
		// Hook mode is checked before ownership on purpose: a hook-mode server
		// injects session_id into nothing, so ownership is false for EVERY
		// tool. Reversing the order would turn every hook-mode call foreign.
		{"hook beats foreign", map[string]any{"session_id": "customer-value"}, hookOK, false, "ses_2bw285VY9apdgUgTPXKFnT6P4G0", SessionSourceHook},
		{"hook beats a garbage argument", map[string]any{"session_id": "nope"}, hookOK, true, "ses_2bw285VY9apdgUgTPXKFnT6P4G0", SessionSourceHook},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveSessionHandle(c.args, c.hook, "p", c.ours)
			if got.Source != c.wantSource {
				t.Errorf("source = %q, want %q", got.Source, c.wantSource)
			}
			if c.wantID == minted {
				if !IsValidSessionID(got.SessionID) {
					t.Errorf("minted %q is not a valid session ID", got.SessionID)
				}
			} else if got.SessionID != c.wantID {
				t.Errorf("session = %q, want %q", got.SessionID, c.wantID)
			}
			if want := c.hook != nil; got.HookMode != want {
				t.Errorf("HookMode = %v, want %v", got.HookMode, want)
			}
		})
	}
}

// TestRejectedValueIsNeverStored pins the reason validation exists at all:
// Event.SessionId is exempt from both redaction hooks, so anything adopted
// there is unredactable. A rejected value must not survive anywhere in the
// resolution.
func TestRejectedValueIsNeverStored(t *testing.T) {
	const secret = "sk_live_secret_token"
	res := ResolveSessionHandle(map[string]any{"session_id": secret}, nil, "p", true)
	if strings.Contains(fmt.Sprintf("%+v", res), secret) {
		t.Errorf("rejected value survived into the resolution: %+v", res)
	}
}

func TestResolveSessionHandleHookFallbacks(t *testing.T) {
	// Hook nil/empty/error/panic → silent random mint, still in hook mode: no
	// parameter exists for the agent to echo, so it must not be prompted.
	for _, h := range []func() (string, error){
		func() (string, error) { return "", nil },
		func() (string, error) { return "  ", nil },
		func() (string, error) { return "", errAny },
		func() (string, error) { panic("boom") },
	} {
		r := ResolveSessionHandle(nil, h, "p", true)
		if !IsValidSessionID(r.SessionID) || r.Source != SessionSourceMinted {
			t.Errorf("hook fallback: got %+v", r)
		}
		if !r.HookMode {
			t.Error("a hook-mode fallback mint is still hook mode")
		}
	}
}

func TestClampAgentID(t *testing.T) {
	if got := ClampAgentID("a\r\nb\nc"); got != "a  b c" {
		t.Errorf("newline clamp: %q", got)
	}
	long := strings.Repeat("x", 250)
	if got := ClampAgentID(long); len(got) != 200 {
		t.Errorf("length clamp: %d", len(got))
	}
}

func TestBuildMintBackText(t *testing.T) {
	id := sid("A")
	issued := "[session_id issued — see this tool's session_id parameter description]\n" +
		"session_id: " + id + "\n" +
		"This is the first-call issuance described in this tool's session_id parameter description."
	corrected := "[session_id unrecognized — see this tool's session_id parameter description]\n" +
		"The value sent was not issued by this server. Re-send the session_id issued earlier for this task; if none was issued yet, send start and one will be issued."

	for _, c := range []struct {
		name string
		res  SessionResolution
		want string
	}{
		{"minted announces the handle", SessionResolution{SessionID: id, Source: SessionSourceMinted}, issued},
		{"invalid corrects without issuing", SessionResolution{Source: SessionSourceInvalid}, corrected},
		{"supplied says nothing new", SessionResolution{SessionID: id, Source: SessionSourceSupplied}, ""},
		{"foreign says nothing at all", SessionResolution{Source: SessionSourceForeign}, ""},
		{"hook says nothing", SessionResolution{SessionID: id, Source: SessionSourceHook, HookMode: true}, ""},
		// Hook mode silences even a mint: the fallback mint happens when the
		// hook returns nothing, and no parameter exists for the agent to echo.
		{"hook-mode mint stays silent", SessionResolution{SessionID: id, Source: SessionSourceMinted, HookMode: true}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := BuildMintBackText(c.res); got != c.want {
				t.Errorf("mint-back text:\n got:  %q\n want: %q", got, c.want)
			}
		})
	}
}

// The correction must never hand out a replacement handle: if a value arrived
// at all the agent already has a good one, and issuing a second would split a
// conversation that was never split.
func TestInvalidCorrectionIssuesNoReplacement(t *testing.T) {
	got := BuildMintBackText(SessionResolution{Source: SessionSourceInvalid})
	if validSessionID.FindString(got) != "" {
		t.Errorf("the correction must name no session ID: %q", got)
	}
	if strings.Contains(got, "ses_") {
		t.Errorf("the correction must not look like it issued a handle: %q", got)
	}
}

func TestClampAgentIDNeverSplitsARune(t *testing.T) {
	// 100 three-byte runes = 300 bytes; the 200-byte cut lands mid-rune at
	// byte 200 (66 whole runes = 198 bytes, then 2 bytes of the 67th).
	long := strings.Repeat("→", 100)
	got := ClampAgentID(long)

	if !utf8.ValidString(got) {
		t.Errorf("ClampAgentID produced invalid UTF-8: %q", got)
	}
	if len(got) > 200 {
		t.Errorf("ClampAgentID returned %d bytes, want at most 200", len(got))
	}
	if got != strings.Repeat("→", 66) {
		t.Errorf("ClampAgentID = %q, want the first 66 whole runes", got)
	}
	if strings.HasSuffix(long, got) == false && !strings.HasPrefix(long, got) {
		t.Errorf("ClampAgentID must return a prefix of its input, got %q", got)
	}
}

func TestClampAgentIDKeepsExactlyFittingValues(t *testing.T) {
	exact := strings.Repeat("a", 200)
	if got := ClampAgentID(exact); got != exact {
		t.Errorf("a 200-byte value must survive intact, got %d bytes", len(got))
	}
}

// TestClampAgentIDKeepsAReplacementCharacterAtTheCut pins that the trim loop
// distinguishes a genuine U+FFFD from the tail of a split sequence. Both
// decode to RuneError; only the split one has size 1.
//
// The value must be LONGER than the budget or the early exit returns before
// the loop ever runs — the case is only meaningful when the cut actually
// happens, and it has to land exactly on the far side of a whole U+FFFD.
func TestClampAgentIDKeepsAReplacementCharacterAtTheCut(t *testing.T) {
	// 197 ASCII + U+FFFD (3 bytes) = exactly 200, then more to force the cut.
	input := strings.Repeat("a", 197) + "�" + strings.Repeat("b", 50)
	if len(input) <= maxAgentIDBytes {
		t.Fatalf("fixture must exceed the budget to reach the trim loop, got %d bytes", len(input))
	}

	want := strings.Repeat("a", 197) + "�"
	got := ClampAgentID(input)
	if got != want {
		t.Errorf("ClampAgentID = %q (%d bytes), want the whole trailing U+FFFD kept (%d bytes)",
			got, len(got), len(want))
	}
	if !utf8.ValidString(got) {
		t.Errorf("ClampAgentID produced invalid UTF-8: %q", got)
	}
}
