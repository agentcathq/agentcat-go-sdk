package truncation

import (
	"fmt"
	"math"
	"strings"
	"testing"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
)

func strPtr(s string) *string { return &s }

func TestTruncateEvent_NilEvent(t *testing.T) {
	TruncateEvent(nil) // must not panic
}

func TestTruncateEvent_FieldLevelLimits(t *testing.T) {
	evt := &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			UserIntent:    strPtr(strings.Repeat("u", 3000)),
			ResourceName:  strPtr(strings.Repeat("r", 300)),
			ServerName:    strPtr(strings.Repeat("s", 300)),
			ServerVersion: strPtr(strings.Repeat("v", 300)),
			ClientName:    strPtr(strings.Repeat("c", 300)),
			ClientVersion: strPtr(strings.Repeat("w", 300)),
		},
	}

	TruncateEvent(evt)

	checks := []struct {
		name  string
		value *string
		want  int
	}{
		{"UserIntent", evt.UserIntent, 2048 + len("...")},
		{"ResourceName", evt.ResourceName, 256 + len("...")},
		{"ServerName", evt.ServerName, 256 + len("...")},
		{"ServerVersion", evt.ServerVersion, 256 + len("...")},
		{"ClientName", evt.ClientName, 256 + len("...")},
		{"ClientVersion", evt.ClientVersion, 256 + len("...")},
	}
	for _, c := range checks {
		if c.value == nil {
			t.Errorf("%s is nil", c.name)
			continue
		}
		if len(*c.value) != c.want {
			t.Errorf("%s length = %d, want %d", c.name, len(*c.value), c.want)
		}
		if !strings.HasSuffix(*c.value, "...") {
			t.Errorf("%s missing truncation suffix", c.name)
		}
	}
}

func TestTruncateEvent_ShortFieldsUntouched(t *testing.T) {
	evt := &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			UserIntent:   strPtr("short intent"),
			ResourceName: strPtr("my_tool"),
		},
	}

	TruncateEvent(evt)

	if *evt.UserIntent != "short intent" {
		t.Errorf("UserIntent changed: %q", *evt.UserIntent)
	}
	if *evt.ResourceName != "my_tool" {
		t.Errorf("ResourceName changed: %q", *evt.ResourceName)
	}
}

func TestTruncateEvent_ErrorMessageAndFrames(t *testing.T) {
	frames := make([]any, 80)
	for i := range frames {
		frames[i] = map[string]any{"function": fmt.Sprintf("fn%d", i)}
	}

	evt := &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Error: map[string]any{
				"message": strings.Repeat("e", 3000),
				"frames":  frames,
			},
		},
	}

	TruncateEvent(evt)

	msg := evt.Error["message"].(string)
	if len(msg) != 2048+len("...") {
		t.Errorf("error message length = %d, want %d", len(msg), 2048+len("..."))
	}

	gotFrames := evt.Error["frames"].([]any)
	if len(gotFrames) != 50 {
		t.Fatalf("frames length = %d, want 50", len(gotFrames))
	}
	first := gotFrames[0].(map[string]any)
	last := gotFrames[49].(map[string]any)
	if first["function"] != "fn0" {
		t.Errorf("first frame = %v, want fn0", first["function"])
	}
	if last["function"] != "fn79" {
		t.Errorf("last frame = %v, want fn79", last["function"])
	}
}

func TestTruncateEvent_ResponseContentTextLimit(t *testing.T) {
	evt := &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Response: map[string]any{
				"content": []any{
					map[string]any{"type": "text", "text": strings.Repeat("t", MaxStringLength+100)},
					map[string]any{"type": "text", "text": "short"},
				},
			},
		},
	}

	TruncateEvent(evt)

	content := evt.Response["content"].([]any)
	long := content[0].(map[string]any)["text"].(string)
	if len(long) != MaxStringLength+len("...") {
		t.Errorf("long text block length = %d, want %d", len(long), MaxStringLength+len("..."))
	}
	if content[1].(map[string]any)["text"] != "short" {
		t.Error("short text block changed")
	}
}

func TestNormalize_Depth(t *testing.T) {
	// Build a nested map deeper than MaxDepth.
	deepest := map[string]any{"leaf": "value"}
	current := deepest
	for i := 0; i < 15; i++ {
		current = map[string]any{"nested": current}
	}

	result := Normalize(current, MaxDepth).(map[string]any)

	// Walk down MaxDepth levels; the next level should be "[Object]".
	node := result
	for i := 0; i < MaxDepth-1; i++ {
		next, ok := node["nested"].(map[string]any)
		if !ok {
			t.Fatalf("expected map at level %d, got %T", i, node["nested"])
		}
		node = next
	}
	if node["nested"] != "[Object]" {
		t.Errorf("expected [Object] placeholder at depth limit, got %v", node["nested"])
	}
}

func TestNormalize_Breadth(t *testing.T) {
	wide := make(map[string]any, 150)
	for i := 0; i < 150; i++ {
		wide[fmt.Sprintf("key%03d", i)] = i
	}

	result := Normalize(wide, MaxDepth).(map[string]any)
	if len(result) != MaxBreadth+1 {
		t.Errorf("map breadth = %d, want %d", len(result), MaxBreadth+1)
	}
	if result["..."] != "[MaxProperties ~]" {
		t.Error("missing breadth marker")
	}

	wideSlice := make([]any, 150)
	for i := range wideSlice {
		wideSlice[i] = i
	}
	sliceResult := Normalize(wideSlice, MaxDepth).([]any)
	if len(sliceResult) != MaxBreadth+1 {
		t.Errorf("slice breadth = %d, want %d", len(sliceResult), MaxBreadth+1)
	}
	if sliceResult[MaxBreadth] != "[MaxProperties ~]" {
		t.Error("missing slice breadth marker")
	}
}

func TestNormalize_Strings(t *testing.T) {
	long := strings.Repeat("s", MaxStringLength+10)
	result := Normalize(long, MaxDepth).(string)
	if len(result) != MaxStringLength+len("...") {
		t.Errorf("string length = %d, want %d", len(result), MaxStringLength+len("..."))
	}
}

func TestNormalize_SpecialFloats(t *testing.T) {
	in := map[string]any{
		"nan":    math.NaN(),
		"posInf": math.Inf(1),
		"negInf": math.Inf(-1),
		"normal": 3.14,
	}
	result := Normalize(in, MaxDepth).(map[string]any)
	if result["nan"] != "[NaN]" {
		t.Errorf("NaN = %v", result["nan"])
	}
	if result["posInf"] != "[Infinity]" {
		t.Errorf("Infinity = %v", result["posInf"])
	}
	if result["negInf"] != "[-Infinity]" {
		t.Errorf("-Infinity = %v", result["negInf"])
	}
	if result["normal"] != 3.14 {
		t.Errorf("normal float = %v", result["normal"])
	}
}

func TestNormalize_CircularReference(t *testing.T) {
	m := map[string]any{"a": "b"}
	m["self"] = m

	result := Normalize(m, MaxDepth).(map[string]any)
	if result["self"] != "[Circular ~]" {
		t.Errorf("circular reference = %v, want [Circular ~]", result["self"])
	}
	if result["a"] != "b" {
		t.Errorf("non-circular value changed: %v", result["a"])
	}
}

func TestNormalize_StructsViaJSONRoundTrip(t *testing.T) {
	type inner struct {
		Text string `json:"text"`
	}
	in := map[string]any{"struct": inner{Text: strings.Repeat("x", MaxStringLength+50)}}

	result := Normalize(in, MaxDepth).(map[string]any)
	converted, ok := result["struct"].(map[string]any)
	if !ok {
		t.Fatalf("struct not converted to map: %T", result["struct"])
	}
	if len(converted["text"].(string)) != MaxStringLength+len("...") {
		t.Error("string inside struct was not truncated")
	}
}

func TestTruncateToSize_DepthReduction(t *testing.T) {
	// Build parameters that are large due to deep nesting with many strings.
	params := map[string]any{}
	node := params
	for depth := 0; depth < 9; depth++ {
		entries := map[string]any{}
		for i := 0; i < 50; i++ {
			entries[fmt.Sprintf("k%02d", i)] = strings.Repeat("v", 100)
		}
		entries["child"] = map[string]any{}
		node["level"] = entries
		node = entries["child"].(map[string]any)
	}

	evt := &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Parameters: params,
		},
	}

	TruncateEvent(evt)

	if size := eventByteSize(evt); size > MaxEventBytes {
		t.Errorf("event size = %d, want <= %d", size, MaxEventBytes)
	}
}

func TestTruncateToSize_LargestStringTruncation(t *testing.T) {
	// A single flat huge string field (depth reduction alone cannot help;
	// per-string normalization caps at 32KB, so use several large strings).
	params := map[string]any{}
	for i := 0; i < 10; i++ {
		params[fmt.Sprintf("blob%d", i)] = strings.Repeat("x", 30000)
	}

	evt := &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Parameters: params,
		},
	}

	TruncateEvent(evt)

	if size := eventByteSize(evt); size > MaxEventBytes {
		t.Errorf("event size = %d, want <= %d", size, MaxEventBytes)
	}
}

func TestTruncateEvent_SmallEventUnchangedBySizeBudget(t *testing.T) {
	evt := &core.Event{
		PublishEventRequest: agentcatapi.PublishEventRequest{
			Parameters: map[string]any{"key": "value"},
		},
	}

	TruncateEvent(evt)

	if evt.Parameters["key"] != "value" {
		t.Errorf("small event was modified: %v", evt.Parameters)
	}
}
