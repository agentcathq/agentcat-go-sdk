package inject

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) *SchemaObject {
	t.Helper()
	o, err := ParseSchemaObject([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func marshalStr(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func defaultCfg() Config {
	return Config{InjectHandles: true, InjectContext: true, ContextDescription: "ctx-desc"}
}

func TestBuildOrderingAndRegistry(t *testing.T) {
	in := []NormalizedTool{{
		Name:        "todo",
		InputSchema: mustParse(t, `{"type":"object","properties":{"zebra":{"type":"string"},"apple":{"type":"number"}},"required":["zebra"]}`),
	}}
	cfg := defaultCfg()
	cfg.AgentTracking = true
	out, reg := Build(cfg, in)

	props := propertiesOf(t, out[0].InputSchema)
	wantOrder := []string{"zebra", "apple", "session_id", "agent_id", "context"}
	if got := props.Keys(); strings.Join(got, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("property order = %v, want %v", got, wantOrder)
	}
	// session_id never required; agent_id and context appended to required.
	var required []string
	rawReq, _ := out[0].InputSchema.Get("required")
	_ = json.Unmarshal(rawReq, &required)
	if strings.Join(required, ",") != "zebra,agent_id,context" {
		t.Errorf("required = %v", required)
	}
	if got := reg.InjectedParams["todo"]; strings.Join(got, ",") != "session_id,agent_id,context" {
		t.Errorf("registry = %v", got)
	}
	// Original input untouched (deep copy).
	origProps := propertiesOf(t, in[0].InputSchema)
	if origProps.Has("session_id") {
		t.Error("Build mutated the input tool")
	}
}

func TestBuildHookModeSkipsSessionID(t *testing.T) {
	cfg := defaultCfg()
	cfg.HookMode = true
	cfg.AgentTracking = true
	out, reg := Build(cfg, []NormalizedTool{{
		Name:        "t",
		InputSchema: mustParse(t, `{"type":"object","properties":{}}`),
	}})
	props := propertiesOf(t, out[0].InputSchema)
	if props.Has("session_id") {
		t.Error("hook mode must not inject session_id")
	}
	// Hook-mode agent_id description differs from default mode.
	raw, _ := props.Get("agent_id")
	if !strings.Contains(string(raw), "distinguishes you from every other agent using agent_id") {
		t.Errorf("agent_id must use hook-mode description, got %s", raw)
	}
	if got := reg.InjectedParams["t"]; strings.Join(got, ",") != "agent_id,context" {
		t.Errorf("registry = %v", got)
	}
}

func TestBuildRemovesAdditionalPropertiesFalseOnly(t *testing.T) {
	out, _ := Build(defaultCfg(), []NormalizedTool{
		{Name: "a", InputSchema: mustParse(t, `{"type":"object","additionalProperties":false,"properties":{}}`)},
		{Name: "b", InputSchema: mustParse(t, `{"type":"object","additionalProperties":{"type":"string"},"properties":{}}`)},
	})
	if out[0].InputSchema.Has("additionalProperties") {
		t.Error("additionalProperties:false must be removed")
	}
	if !out[1].InputSchema.Has("additionalProperties") {
		t.Error("non-false additionalProperties must be preserved")
	}
}

func TestBuildSkipsComposedInputSchemas(t *testing.T) {
	orig := `{"oneOf":[{"type":"object"},{"type":"object"}]}`
	out, reg := Build(defaultCfg(), []NormalizedTool{{Name: "c", InputSchema: mustParse(t, orig)}})
	if marshalStr(t, out[0].InputSchema) != orig {
		t.Error("composed schema must pass through untouched")
	}
	if got, ok := reg.InjectedParams["c"]; !ok || len(got) != 0 {
		t.Errorf("composed tool must get an empty registry entry, got %v ok=%v", got, ok)
	}
}

func TestBuildCollisionSkipsThatParamOnly(t *testing.T) {
	out, reg := Build(defaultCfg(), []NormalizedTool{{
		Name:        "t",
		InputSchema: mustParse(t, `{"type":"object","properties":{"session_id":{"type":"integer","description":"customer's own"}}}`),
	}})
	props := propertiesOf(t, out[0].InputSchema)
	raw, _ := props.Get("session_id")
	if !strings.Contains(string(raw), "customer's own") {
		t.Error("customer's session_id must survive untouched")
	}
	if got := reg.InjectedParams["t"]; strings.Join(got, ",") != "context" {
		t.Errorf("only context should be recorded, got %v", got)
	}
}

func TestBuildOutputSchemaInjection(t *testing.T) {
	out, reg := Build(defaultCfg(), []NormalizedTool{
		{Name: "declared", InputSchema: mustParse(t, `{"type":"object","properties":{}}`),
			OutputSchema: mustParse(t, `{"type":"object","properties":{"answer":{"type":"string"}},"additionalProperties":false}`)},
		{Name: "none", InputSchema: mustParse(t, `{"type":"object","properties":{}}`)},
		{Name: "composed", InputSchema: mustParse(t, `{"type":"object","properties":{}}`),
			OutputSchema: mustParse(t, `{"oneOf":[{"type":"object"}]}`)},
		{Name: "customerkey", InputSchema: mustParse(t, `{"type":"object","properties":{}}`),
			OutputSchema: mustParse(t, `{"type":"object","properties":{"_mcp_instructions":{"type":"string"}}}`)},
	})
	if !reg.OutputInjected["declared"] {
		t.Error("declared plain-object outputSchema must be extended")
	}
	oProps := propertiesOf(t, out[0].OutputSchema)
	raw, _ := oProps.Get("_mcp_instructions")
	for _, want := range []string{"Read and follow.", "session_id argument", "a subagent must generate its own"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("_mcp_instructions schema missing %q: %s", want, raw)
		}
	}
	// Output additionalProperties:false must be preserved (declared property suffices).
	if !out[0].OutputSchema.Has("additionalProperties") {
		t.Error("output additionalProperties must not be touched")
	}
	for _, name := range []string{"none", "composed", "customerkey"} {
		if reg.OutputInjected[name] {
			t.Errorf("%s must not be in the output registry", name)
		}
	}
	// Customer's own _mcp_instructions declaration wins, untouched.
	cProps := propertiesOf(t, out[3].OutputSchema)
	raw, _ = cProps.Get("_mcp_instructions")
	if string(raw) != `{"type":"string"}` {
		t.Errorf("customer _mcp_instructions changed: %s", raw)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	tools := []NormalizedTool{{
		Name:         "t",
		InputSchema:  mustParse(t, `{"type":"object","properties":{"q":{"type":"string"}}}`),
		OutputSchema: mustParse(t, `{"type":"object","properties":{}}`),
	}}
	cfg := defaultCfg()
	cfg.AgentTracking = true
	out1, reg1 := Build(cfg, tools)
	out2, reg2 := Build(cfg, tools)
	if marshalStr(t, out1) != marshalStr(t, out2) {
		t.Error("Build output must be deterministic")
	}
	if marshalStr(t, reg1) != marshalStr(t, reg2) {
		t.Error("Build registries must be deterministic")
	}
}

func TestBuildHandlesDisabledInjectsOnlyContext(t *testing.T) {
	cfg := Config{InjectHandles: false, InjectContext: true, ContextDescription: "d", AgentTracking: true}
	out, reg := Build(cfg, []NormalizedTool{{Name: "t", InputSchema: mustParse(t, `{"type":"object","properties":{}}`),
		OutputSchema: mustParse(t, `{"type":"object","properties":{}}`)}})
	props := propertiesOf(t, out[0].InputSchema)
	if props.Has("session_id") || props.Has("agent_id") {
		t.Error("handles must not be injected when disabled")
	}
	if !props.Has("context") {
		t.Error("context must still be injected")
	}
	if len(reg.OutputInjected) != 0 {
		t.Error("no output injection when handles are disabled")
	}
}

// propertiesOf parses the "properties" member of a schema.
func propertiesOf(t *testing.T, s *SchemaObject) *SchemaObject {
	t.Helper()
	raw, ok := s.Get("properties")
	if !ok {
		t.Fatal("schema has no properties")
	}
	return mustParse(t, string(raw))
}

// TestBuildIsIdempotent pins the contract that lets an adapter declare the
// injected parameters on its library's own registered schema: running Build
// over its own output must re-assert what it injected, not mistake it for a
// customer-owned collision and silently empty the registry (which is what
// drives argument stripping).
func TestBuildIsIdempotent(t *testing.T) {
	cfg := defaultCfg()
	cfg.AgentTracking = true
	tools := []NormalizedTool{{
		Name:         "t",
		InputSchema:  mustParse(t, `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`),
		OutputSchema: mustParse(t, `{"type":"object","properties":{"a":{"type":"string"}}}`),
	}}

	once, reg1 := Build(cfg, tools)
	twice, reg2 := Build(cfg, once)

	if marshalStr(t, once) != marshalStr(t, twice) {
		t.Errorf("Build is not idempotent:\n once: %s\ntwice: %s", marshalStr(t, once), marshalStr(t, twice))
	}
	if marshalStr(t, reg1) != marshalStr(t, reg2) {
		t.Errorf("registries differ on the second pass:\n once: %s\ntwice: %s", marshalStr(t, reg1), marshalStr(t, reg2))
	}
	if got := reg2.InjectedParams["t"]; strings.Join(got, ",") != "session_id,agent_id,context" {
		t.Errorf("second pass must re-assert the injected params, got %v", got)
	}
	if !reg2.OutputInjected["t"] {
		t.Error("second pass must re-assert the output injection")
	}
	var required []string
	rawReq, _ := twice[0].InputSchema.Get("required")
	_ = json.Unmarshal(rawReq, &required)
	if strings.Join(required, ",") != "q,agent_id,context" {
		t.Errorf("required must not accumulate duplicates, got %v", required)
	}
}

// TestBuildIdempotenceRespectsADifferentDescription pins that the re-assert
// rule is byte-exact: a property that merely shares a NAME with an injected
// one is still the customer's.
func TestBuildIdempotenceRespectsADifferentDescription(t *testing.T) {
	cfg := defaultCfg()
	out, reg := Build(cfg, []NormalizedTool{{
		Name:        "t",
		InputSchema: mustParse(t, `{"type":"object","properties":{"context":{"type":"string","description":"mine"}}}`),
	}})
	props := propertiesOf(t, out[0].InputSchema)
	raw, _ := props.Get("context")
	if !strings.Contains(string(raw), "mine") {
		t.Errorf("customer's context must survive untouched: %s", raw)
	}
	if got := reg.InjectedParams["t"]; strings.Join(got, ",") != "session_id" {
		t.Errorf("only session_id should be recorded, got %v", got)
	}
}

func TestBuildLeavesOpaqueSchemasAlone(t *testing.T) {
	out, reg := Build(defaultCfg(), []NormalizedTool{{
		Name:               "opaque",
		InputSchemaOpaque:  true,
		OutputSchemaOpaque: true,
	}})
	if out[0].InputSchema != nil || out[0].OutputSchema != nil {
		t.Error("an opaque schema must never be replaced with a synthesised one")
	}
	if !out[0].InputSchemaOpaque || !out[0].OutputSchemaOpaque {
		t.Error("the opaque markers must survive Build so the adapter skips write-back")
	}
	if got, ok := reg.InjectedParams["opaque"]; !ok || len(got) != 0 {
		t.Errorf("opaque tool must get an empty registry entry, got %v ok=%v", got, ok)
	}
	if reg.OutputInjected["opaque"] {
		t.Error("an opaque output schema must not be mirrored into")
	}
}

func TestBuildLeavesSchemalessToolsAlone(t *testing.T) {
	out, reg := Build(defaultCfg(), []NormalizedTool{{Name: "bare"}})
	if out[0].InputSchema != nil {
		t.Errorf("a tool that declares no input schema must not be given one: %s", marshalStr(t, out[0].InputSchema))
	}
	if got, ok := reg.InjectedParams["bare"]; !ok || len(got) != 0 {
		t.Errorf("schemaless tool must get an empty registry entry, got %v ok=%v", got, ok)
	}
}

func TestInjectOutputSkipsNonObjectSchemas(t *testing.T) {
	out, reg := Build(defaultCfg(), []NormalizedTool{
		{Name: "arr", InputSchema: mustParse(t, `{"type":"object","properties":{}}`),
			OutputSchema: mustParse(t, `{"type":"array","items":{"type":"string"}}`)},
		{Name: "prim", InputSchema: mustParse(t, `{"type":"object","properties":{}}`),
			OutputSchema: mustParse(t, `{"type":"string"}`)},
		{Name: "union", InputSchema: mustParse(t, `{"type":"object","properties":{}}`),
			OutputSchema: mustParse(t, `{"type":["object","null"],"properties":{}}`)},
		{Name: "untyped", InputSchema: mustParse(t, `{"type":"object","properties":{}}`),
			OutputSchema: mustParse(t, `{"properties":{"a":{"type":"string"}}}`)},
	})
	for i, name := range []string{"arr", "prim"} {
		if reg.OutputInjected[name] {
			t.Errorf("%s: a non-object output schema must not be extended", name)
		}
		if out[i].OutputSchema.Has("properties") && name == "arr" {
			t.Errorf("%s: properties must not be added: %s", name, marshalStr(t, out[i].OutputSchema))
		}
	}
	if !reg.OutputInjected["union"] {
		t.Error("a type union that permits object must still be extended")
	}
	if !reg.OutputInjected["untyped"] {
		t.Error("a schema with no declared type constrains nothing and must still be extended")
	}
}

func TestMergeRegistries(t *testing.T) {
	prev := &Registries{
		InjectedParams: map[string][]string{"page1": {"session_id", "context"}, "shared": {"session_id"}},
		OutputInjected: map[string]bool{"page1": true, "shared": true},
	}
	next := &Registries{
		InjectedParams: map[string][]string{"page2": {"context"}, "shared": {"context"}},
		OutputInjected: map[string]bool{"page2": true},
	}

	merged := MergeRegistries(prev, next)

	if got := merged.InjectedParams["page1"]; strings.Join(got, ",") != "session_id,context" {
		t.Errorf("a tool absent from the newer list must keep its entry, got %v", got)
	}
	if got := merged.InjectedParams["page2"]; strings.Join(got, ",") != "context" {
		t.Errorf("the newer list's entries must be present, got %v", got)
	}
	if got := merged.InjectedParams["shared"]; strings.Join(got, ",") != "context" {
		t.Errorf("the newer list must win per tool (no stale union), got %v", got)
	}
	if !merged.OutputInjected["page1"] {
		t.Error("an absent tool's mirror gate must survive")
	}
	if merged.OutputInjected["shared"] {
		t.Error("a tool the newer list no longer output-injects must lose its mirror gate")
	}
	// Inputs untouched: another list may be racing on the same slot.
	if len(prev.InjectedParams) != 2 || len(next.InjectedParams) != 2 {
		t.Error("MergeRegistries must not mutate its inputs")
	}
	if got := MergeRegistries(nil, next); got != next {
		t.Error("merging into nil must return the newer registries")
	}
	if got := MergeRegistries(prev, nil); got != prev {
		t.Error("merging nil must leave the previous registries in place")
	}
}

func TestSessionParamIsOurs(t *testing.T) {
	reg := &Registries{InjectedParams: map[string][]string{
		"injected":  {"session_id", "agent_id", "context"},
		"agentonly": {"agent_id", "context"},
		"composed":  {},
	}}
	for _, c := range []struct {
		name string
		tool string
		reg  *Registries
		want bool
	}{
		// A call can land before any tools/list — a fresh per-request factory
		// instance, a client that skips discovery. Disowning it there would
		// silently stop validating every such call, so unknown means ours.
		{"nil registry", "anything", nil, true},
		{"tool never listed", "unseen", reg, true},
		{"session_id injected", "injected", reg, true},
		// The customer declares session_id themselves, so Build skipped it.
		{"customer declares it", "agentonly", reg, false},
		// Build also skips injection wholesale for composed, opaque and
		// malformed input schemas, which land here as an empty entry. Strict
		// parity with the TypeScript SDK treats those as foreign too: the
		// tag reads "foreign" and the call publishes sessionless.
		{"composed schema", "composed", reg, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := SessionParamIsOurs(c.tool, c.reg); got != c.want {
				t.Errorf("SessionParamIsOurs(%q) = %v, want %v", c.tool, got, c.want)
			}
		})
	}
}

// TestBuildRecordsCustomerDeclaredSessionParam pins the data the ownership
// question is answered from: a tool declaring its own session_id must be
// listed in InjectedParams (so it counts as "seen") without session_id among
// its entries (so it counts as the customer's).
func TestBuildRecordsCustomerDeclaredSessionParam(t *testing.T) {
	own, err := ParseSchemaObject([]byte(`{"type":"object","properties":{"session_id":{"type":"string","description":"mine"}}}`))
	if err != nil {
		t.Fatalf("ParseSchemaObject: %v", err)
	}
	cfg := Config{InjectHandles: true, InjectContext: true, ContextDescription: "why"}

	_, reg := Build(cfg, []NormalizedTool{{Name: "own", InputSchema: own}})

	params, seen := reg.InjectedParams["own"]
	if !seen {
		t.Fatal("a listed tool must appear in InjectedParams even when nothing was injected")
	}
	for _, p := range params {
		if p == "session_id" {
			t.Error("session_id must not be recorded as injected when the customer declares it")
		}
	}
	if SessionParamIsOurs("own", reg) {
		t.Error("a customer-declared session_id is not ours to read")
	}
	if !slices.Contains(reg.CustomerOwnedParams["own"], "session_id") {
		t.Errorf("the collision must be recorded as data for the call path to report: %v", reg.CustomerOwnedParams)
	}
}

// TestBuildStaysDeterministicWithCollisions pins that recording the collision
// did not make Build stateful. Build runs on every tools/list and every
// on-demand rebuild, and rebuild-on-demand depends on identical config plus
// identical tools producing identical registries.
func TestBuildStaysDeterministicWithCollisions(t *testing.T) {
	raw := `{"type":"object","properties":{"session_id":{"type":"string","description":"mine"},"q":{"type":"string"}}}`
	cfg := Config{InjectHandles: true, AgentTracking: true, InjectContext: true, ContextDescription: "why"}

	build := func() *Registries {
		own, err := ParseSchemaObject([]byte(raw))
		if err != nil {
			t.Fatalf("ParseSchemaObject: %v", err)
		}
		_, reg := Build(cfg, []NormalizedTool{{Name: "own", InputSchema: own}})
		return reg
	}
	a, b := build(), build()
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Build is not deterministic:\n first:  %+v\n second: %+v", a, b)
	}
	if len(a.CustomerOwnedParams["own"]) != 1 {
		t.Errorf("a repeated build must not accumulate entries: %v", a.CustomerOwnedParams)
	}
}

// A tool re-registered without its own session_id must lose the recorded
// collision, or the error would keep firing after the customer fixed it.
func TestMergeRegistriesClearsAFixedCollision(t *testing.T) {
	prev := &Registries{
		InjectedParams:      map[string][]string{"t": {}},
		OutputInjected:      map[string]bool{},
		CustomerOwnedParams: map[string][]string{"t": {"session_id"}},
	}
	next := &Registries{
		InjectedParams:      map[string][]string{"t": {"session_id", "context"}},
		OutputInjected:      map[string]bool{},
		CustomerOwnedParams: map[string][]string{},
	}
	merged := MergeRegistries(prev, next)
	if got := merged.CustomerOwnedParams["t"]; len(got) != 0 {
		t.Errorf("a fixed collision must not persist: %v", got)
	}
	if !SessionParamIsOurs("t", merged) {
		t.Error("after the fix, session_id is ours again")
	}
}
