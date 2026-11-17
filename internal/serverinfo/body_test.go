package serverinfo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// pages serves items in pages of size, with numbered cursors.
func pages(size int, items ...any) ListFunc {
	return func(_ context.Context, cursor string) (Page, error) {
		start := 0
		if cursor != "" {
			fmt.Sscanf(cursor, "c%d", &start)
		}
		end := start + size
		if end >= len(items) {
			return Page{Items: items[start:]}, nil
		}
		return Page{Items: items[start:end], NextCursor: fmt.Sprintf("c%d", end)}, nil
	}
}

func failing(err error) ListFunc {
	return func(context.Context, string) (Page, error) { return Page{}, err }
}

func item(fields ...string) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(fields); i += 2 {
		m[fields[i]] = fields[i+1]
	}
	return m
}

var demoTarget = Target{ProjectID: "proj_" + strings.Repeat("a", 27), ServerName: "demo-server"}

func TestBody_V1_HoldsTheSixKeysAndEveryList(t *testing.T) {
	long := strings.Repeat("d", 2000)
	src := &Source{
		ServerVersion: "1.2.3",
		ServerTitle:   "Demo Title",
		Instructions:  "Use the demo tools.",
		Tools: pages(10,
			item("name", "alpha"),
			map[string]any{"name": "beta", "description": long, "inputSchema": map[string]any{"type": "object"}},
			item("name", "gamma"),
			item("name", "get_more_tools")),
		Resources:         pages(10, item("uri", "file:///a"), item("uri", "file:///b")),
		ResourceTemplates: pages(10, item("uriTemplate", "file:///{x}")),
		Prompts:           pages(10, item("name", "p1"), item("name", "p2")),
	}
	body, err := buildBody(context.Background(), demoTarget, src)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	raw, _ := json.Marshal(body)
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"project_id", "server_name", "server_version", "server_title", "instructions", "features"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("body has no key %q: %s", key, raw)
		}
	}
	features := decoded["features"].(map[string]any)
	for key, want := range map[string]int{KeyTools: 4, KeyResources: 2, KeyResourceTemplates: 1, KeyPrompts: 2} {
		if got := len(features[key].([]any)); got != want {
			t.Errorf("%s has %d items, want %d", key, got, want)
		}
	}
	beta := features[KeyTools].([]any)[1].(map[string]any)
	if beta["description"] != long {
		t.Error("the 2,000-character description did not arrive unchanged")
	}
}

func TestBody_V2_FollowsEveryPage(t *testing.T) {
	src := &Source{Tools: pages(2,
		item("name", "t1"), item("name", "t2"), item("name", "t3"),
		item("name", "t4"), item("name", "get_more_tools"))}
	body, err := buildBody(context.Background(), demoTarget, src)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	tools := body.Features[KeyTools].([]any)
	if len(tools) != 5 {
		t.Fatalf("tools = %d, want 5 from 3 pages", len(tools))
	}
	if got := tools[4].(map[string]any)["name"]; got != "get_more_tools" {
		t.Errorf("last tool = %v, want get_more_tools once", got)
	}
}

func TestBody_V3_NoHandlerMeansNoKey(t *testing.T) {
	src := &Source{Tools: pages(10, item("name", "alpha"))}
	body, err := buildBody(context.Background(), demoTarget, src)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	if len(body.Features) != 1 || body.Features[KeyTools] == nil {
		t.Fatalf("features = %v, want only tools", body.Features)
	}
}

func TestBody_EmptyListGivesEmptyArray(t *testing.T) {
	src := &Source{Tools: pages(10, item("name", "alpha")), Prompts: pages(10)}
	body, err := buildBody(context.Background(), demoTarget, src)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	raw, _ := json.Marshal(body.Features)
	if !strings.Contains(string(raw), `"prompts":[]`) {
		t.Fatalf("features = %s, want an empty prompts array", raw)
	}
}

func TestBody_V4_FailedListIsLeftOut(t *testing.T) {
	src := &Source{
		Tools:   pages(10, item("name", "alpha"), item("name", "beta")),
		Prompts: failing(errors.New("prompt handler failed")),
	}
	body, err := buildBody(context.Background(), demoTarget, src)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	if _, ok := body.Features[KeyPrompts]; ok {
		t.Error("features has prompts, want the failed list left out")
	}
	if got := len(body.Features[KeyTools].([]any)); got != 2 {
		t.Errorf("tools = %d, want 2", got)
	}
}

func TestBody_V5_FailedToolListSendsNothing(t *testing.T) {
	src := &Source{Tools: failing(errors.New("tool handler failed")), Prompts: pages(10)}
	if _, err := buildBody(context.Background(), demoTarget, src); err == nil {
		t.Fatal("buildBody succeeded, want an error when the tool list fails")
	}
	if _, err := buildBody(context.Background(), demoTarget, &Source{}); err == nil {
		t.Fatal("buildBody succeeded, want an error when the server has no tool list")
	}
}

func TestBody_V6_RepeatedCursorFails(t *testing.T) {
	var calls int
	repeat := func(context.Context, string) (Page, error) {
		calls++
		return Page{Items: []any{item("name", "x")}, NextCursor: "same"}, nil
	}
	start := time.Now()
	_, err := collectList(context.Background(), repeat)
	if !errors.Is(err, errCursorRepeated) {
		t.Fatalf("err = %v, want a repeated cursor", err)
	}
	if calls > maxPages {
		t.Errorf("calls = %d, want at most %d", calls, maxPages)
	}
	if elapsed := time.Since(start); elapsed >= listTimeout {
		t.Errorf("took %v, want under %v", elapsed, listTimeout)
	}
}

func TestBody_ListWithMoreThan100PagesFails(t *testing.T) {
	items := make([]any, 101)
	for i := range items {
		items[i] = item("name", fmt.Sprint(i))
	}
	if _, err := collectList(context.Background(), pages(1, items...)); !errors.Is(err, errTooManyPages) {
		t.Fatalf("err = %v, want too many pages", err)
	}
	if got, err := collectList(context.Background(), pages(1, items[:100]...)); err != nil || len(got) != 100 {
		t.Fatalf("100 pages: got %d items, err %v", len(got), err)
	}
}

func TestBody_ListThatPanicsFails(t *testing.T) {
	boom := func(context.Context, string) (Page, error) { panic("handler exploded") }
	if _, err := collectList(context.Background(), boom); err == nil {
		t.Fatal("want an error from a panicking handler")
	}
}

func TestBody_ListThatNeverReturnsFailsAtItsTimeLimit(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	hang := func(context.Context, string) (Page, error) {
		<-release
		return Page{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := collectList(ctx, hang); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the time limit", err)
	}
}

func TestBody_FinishToolsAndDropUnsupported(t *testing.T) {
	src := &Source{
		Tools:     pages(10, item("name", "alpha")),
		Resources: pages(10),
		FinishTools: func(tools []any) ([]any, error) {
			return append(tools, item("name", "added")), nil
		},
		DropUnsupported: func(features map[string][]any) { delete(features, KeyResources) },
	}
	body, err := buildBody(context.Background(), demoTarget, src)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	if got := len(body.Features[KeyTools].([]any)); got != 2 {
		t.Errorf("tools = %d, want 2 after FinishTools", got)
	}
	if _, ok := body.Features[KeyResources]; ok {
		t.Error("resources kept, want it dropped")
	}
}

func TestBody_NumbersKeepTheirLiteral(t *testing.T) {
	src := &Source{Tools: pages(10, json.RawMessage(`{"name":"n","inputSchema":{"maximum":9007199254740993}}`))}
	body, err := buildBody(context.Background(), demoTarget, src)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	raw, _ := json.Marshal(body.Features)
	if !strings.Contains(string(raw), "9007199254740993") {
		t.Fatalf("features = %s, want the integer literal unchanged", raw)
	}
}
