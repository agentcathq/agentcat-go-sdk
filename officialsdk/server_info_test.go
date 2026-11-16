package officialsdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// ── heartbeat recorder (installed by TestMain) ──────────────────────────────

// heartbeatRecorder stands in for the heartbeat's sender for the whole
// package, so no test sends a heartbeat to the network.
type heartbeatRecorder struct {
	mu     sync.Mutex
	bodies []*agentcat.ServerInfoRequest
	answer func(ctx context.Context) (int, error)
}

var heartbeats = &heartbeatRecorder{}

func (r *heartbeatRecorder) send(ctx context.Context, body *agentcat.ServerInfoRequest) (int, error) {
	r.mu.Lock()
	r.bodies = append(r.bodies, body)
	answer := r.answer
	r.mu.Unlock()
	if answer != nil {
		return answer(ctx)
	}
	return http.StatusAccepted, nil
}

func (r *heartbeatRecorder) answerWith(t *testing.T, answer func(ctx context.Context) (int, error)) {
	t.Helper()
	r.mu.Lock()
	r.answer = answer
	r.mu.Unlock()
	t.Cleanup(func() {
		r.mu.Lock()
		r.answer = nil
		r.mu.Unlock()
	})
}

func (r *heartbeatRecorder) bodiesFor(projectID string) []*agentcat.ServerInfoRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*agentcat.ServerInfoRequest
	for _, b := range r.bodies {
		if b.ProjectId == projectID {
			out = append(out, b)
		}
	}
	return out
}

// ── clock and memory ────────────────────────────────────────────────────────

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// newHeartbeatMemory waits out earlier heartbeats, empties the memory and
// installs a fake clock. The memory starts at age zero.
func newHeartbeatMemory(t *testing.T) *fakeClock {
	t.Helper()
	agentcat.WaitServerInfoForTest(10 * time.Second)
	t.Cleanup(agentcat.SetServerInfoEnabledForTest(true))
	clock := &fakeClock{t: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	t.Cleanup(agentcat.SetServerInfoClockForTest(clock.now))
	agentcat.ResetServerInfoForTest()
	t.Cleanup(agentcat.ResetServerInfoForTest)
	return clock
}

// startMemory runs one check that cannot start a heartbeat, so the memory's
// age counts from now.
func startMemory() {
	agentcat.MaybeStartServerInfo(context.Background(), agentcat.ServerInfoTriggerToolsList,
		&agentcat.AgentCatInstance{}, "", nil)
}

// agedHeartbeatMemory is newHeartbeatMemory with a memory 60 seconds old.
func agedHeartbeatMemory(t *testing.T) *fakeClock {
	t.Helper()
	clock := newHeartbeatMemory(t)
	startMemory()
	clock.advance(60 * time.Second)
	return clock
}

func waitHeartbeats(t *testing.T) {
	t.Helper()
	if !agentcat.WaitServerInfoForTest(10 * time.Second) {
		t.Fatal("a heartbeat did not finish")
	}
}

func projectID(tag string) string {
	id := "proj_" + tag + strings.Repeat("0", 27)
	return id[:5+27]
}

// ── fixture server ──────────────────────────────────────────────────────────

var longDescription = strings.Repeat("The beta tool does one careful thing. ", 60)[:2000]

func textTool(text string) mcp.ToolHandler {
	return func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
}

func objectSchema(props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props}
}

// newDemoServer builds the fixture server: three tools, two resources, one
// resource template and two prompts.
func newDemoServer(options *mcp.ServerOptions) *mcp.Server {
	if options == nil {
		options = &mcp.ServerOptions{}
	}
	if options.Instructions == "" {
		options.Instructions = "Use the demo tools."
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "demo-server", Version: "1.2.3", Title: "Demo Title"}, options)
	s.AddTool(&mcp.Tool{Name: "alpha", Description: "First tool", InputSchema: objectSchema(nil)}, textTool("alpha done"))
	s.AddTool(&mcp.Tool{
		Name:         "beta",
		Title:        "Beta Tool",
		Description:  longDescription,
		InputSchema:  objectSchema(map[string]any{"query": map[string]any{"type": "string", "description": "What to look for"}}),
		OutputSchema: objectSchema(map[string]any{"answer": map[string]any{"type": "string"}}),
		Annotations:  &mcp.ToolAnnotations{ReadOnlyHint: true, Title: "Beta"},
	}, textTool("beta done"))
	s.AddTool(&mcp.Tool{Name: "gamma", Description: "Third tool", InputSchema: objectSchema(nil)}, textTool("gamma done"))

	readNothing := func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{}, nil
	}
	s.AddResource(&mcp.Resource{URI: "file:///readme.txt", Name: "readme", Description: "The readme"}, readNothing)
	s.AddResource(&mcp.Resource{URI: "file:///notes.txt", Name: "notes", MIMEType: "text/plain"}, readNothing)
	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "file:///docs/{id}", Name: "doc"}, readNothing)

	prompt := func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{}, nil
	}
	s.AddPrompt(&mcp.Prompt{Name: "greet", Description: "Say hello", Arguments: []*mcp.PromptArgument{{Name: "who", Required: true}}}, prompt)
	s.AddPrompt(&mcp.Prompt{Name: "plain", Description: "No arguments"}, prompt)
	return s
}

// trackDemo tracks s the way Track does, with events going to a mock
// publisher, and connects a client.
func trackDemo(t *testing.T, s *mcp.Server, project string, opts *Options) (*mcp.ClientSession, *mockPublisher) {
	t.Helper()
	if opts == nil {
		opts = DefaultOptions()
	}
	coreOpts := &agentcat.Options{
		DisableReportMissing:       opts.DisableReportMissing,
		DisableToolCallContext:     opts.DisableToolCallContext,
		DisableTracing:             opts.DisableTracing,
		EnableAgentTracking:        opts.EnableAgentTracking,
		RedactSensitiveInformation: opts.RedactSensitiveInformation,
	}
	agentcat.RegisterServer(s, &agentcat.AgentCatInstance{ProjectID: project, Options: coreOpts})
	t.Cleanup(func() { agentcat.UnregisterServer(s) })
	mock := &mockPublisher{}
	s.AddReceivingMiddleware(newTrackingMiddleware(s, project, opts, mock.publish, getServerImpl(s)))
	registerGetMoreToolsIfEnabled(s, coreOpts)
	return connectClient(t, s), mock
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

func listTools(t *testing.T, cs *mcp.ClientSession) []*mcp.Tool {
	t.Helper()
	var tools []*mcp.Tool
	cursor := ""
	for {
		res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		tools = append(tools, res.Tools...)
		if res.NextCursor == "" {
			return tools
		}
		cursor = res.NextCursor
	}
}

// failList is receiving middleware, added before Track, that fails one list
// method the way a customer's handler would.
func failList(method string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, m string, req mcp.Request) (mcp.Result, error) {
			if m == method {
				return nil, errors.New(method + " failed")
			}
			return next(ctx, m, req)
		}
	}
}

func plainJSON(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func featureItems(t *testing.T, body *agentcat.ServerInfoRequest, key string) []map[string]any {
	t.Helper()
	raw, ok := plainJSON(t, body.Features).(map[string]any)[key].([]any)
	if !ok {
		t.Fatalf("features has no list %q: %v", key, body.Features)
	}
	items := make([]map[string]any, len(raw))
	for i, item := range raw {
		items[i] = item.(map[string]any)
	}
	return items
}

func names(items []map[string]any) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item["name"].(string))
	}
	sort.Strings(out)
	return out
}

func featureKeys(body *agentcat.ServerInfoRequest) []string {
	keys := make([]string, 0, len(body.Features))
	for key := range body.Features {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func onlyBody(t *testing.T, project string) *agentcat.ServerInfoRequest {
	t.Helper()
	waitHeartbeats(t)
	bodies := heartbeats.bodiesFor(project)
	if len(bodies) != 1 {
		t.Fatalf("POSTs = %d, want 1", len(bodies))
	}
	return bodies[0]
}

func expectPosts(t *testing.T, project string, want int) {
	t.Helper()
	waitHeartbeats(t)
	if got := len(heartbeats.bodiesFor(project)); got != want {
		t.Fatalf("POSTs = %d, want %d", got, want)
	}
}

// ── the test table ──────────────────────────────────────────────────────────

func TestServerInfo_V1_OneToolCallSendsWhatAClientSees(t *testing.T) {
	agedHeartbeatMemory(t)
	project := projectID("v1")
	cs, _ := trackDemo(t, newDemoServer(nil), project, nil)

	callTool(t, cs, "alpha", nil)
	body := onlyBody(t, project)

	decoded := plainJSON(t, body).(map[string]any)
	for _, key := range []string{"project_id", "server_name", "server_version", "server_title", "instructions", "features"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("body has no key %q", key)
		}
	}
	if body.GetServerName() != "demo-server" || body.GetServerVersion() != "1.2.3" ||
		body.GetServerTitle() != "Demo Title" || body.GetInstructions() != "Use the demo tools." {
		t.Errorf("identity = %q %q %q %q", body.GetServerName(), body.GetServerVersion(), body.GetServerTitle(), body.GetInstructions())
	}

	want := map[string]any{}
	for _, tool := range listTools(t, cs) {
		want[tool.Name] = plainJSON(t, tool)
	}
	tools := featureItems(t, body, agentcat.ServerInfoKeyTools)
	got := map[string]any{}
	for _, tool := range tools {
		got[tool["name"].(string)] = tool
	}
	for name, tool := range want {
		if !reflect.DeepEqual(got[name], tool) {
			g, _ := json.Marshal(got[name])
			w, _ := json.Marshal(tool)
			t.Errorf("tool %s differs from tools/list:\n got  %s\n want %s", name, g, w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("tools = %v, want %d tools", names(tools), len(want))
	}
	if _, ok := got[agentcat.GetMoreToolsName]; !ok || len(tools) != 4 {
		t.Errorf("tools = %v, want alpha, beta, gamma and get_more_tools", names(tools))
	}
	beta := got["beta"].(map[string]any)
	if beta["description"] != longDescription {
		t.Error("the 2,000-character description did not arrive unchanged")
	}
	props := beta["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props[agentcat.ParamContext]; !ok {
		t.Errorf("beta has no injected context parameter: %v", props)
	}

	for key, n := range map[string]int{
		agentcat.ServerInfoKeyResources:         2,
		agentcat.ServerInfoKeyResourceTemplates: 1,
		agentcat.ServerInfoKeyPrompts:           2,
	} {
		if got := len(featureItems(t, body, key)); got != n {
			t.Errorf("%s has %d items, want %d", key, got, n)
		}
	}
	expectPosts(t, project, 1)
}

func TestServerInfo_V2_ReadsEveryPage(t *testing.T) {
	agedHeartbeatMemory(t)
	project := projectID("v2")
	s := mcp.NewServer(&mcp.Implementation{Name: "paged", Version: "1"}, &mcp.ServerOptions{PageSize: 2})
	for _, name := range []string{"t1", "t2", "t3", "t4"} {
		s.AddTool(&mcp.Tool{Name: name, InputSchema: objectSchema(nil)}, textTool(name))
	}
	cs, _ := trackDemo(t, s, project, nil)

	if res, err := cs.ListTools(context.Background(), nil); err != nil || res.NextCursor == "" {
		t.Fatalf("first page: err %v, cursor %q; want a paged list", err, res.NextCursor)
	}
	tools := featureItems(t, onlyBody(t, project), agentcat.ServerInfoKeyTools)
	want := []string{agentcat.GetMoreToolsName, "t1", "t2", "t3", "t4"}
	if got := names(tools); !reflect.DeepEqual(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

func TestServerInfo_V3_ToolsOnlyServerHasOnlyTheToolsKey(t *testing.T) {
	agedHeartbeatMemory(t)
	project := projectID("v3")
	s := mcp.NewServer(&mcp.Implementation{Name: "tools-only", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "alpha", InputSchema: objectSchema(nil)}, textTool("a"))
	cs, _ := trackDemo(t, s, project, nil)

	callTool(t, cs, "alpha", nil)
	if got := featureKeys(onlyBody(t, project)); !reflect.DeepEqual(got, []string{"tools"}) {
		t.Fatalf("features keys = %v, want [tools]", got)
	}
}

func TestServerInfo_V4_FailedPromptListIsLeftOut(t *testing.T) {
	agedHeartbeatMemory(t)
	project := projectID("v4")
	s := newDemoServer(nil)
	s.AddReceivingMiddleware(failList("prompts/list"))
	cs, _ := trackDemo(t, s, project, nil)

	callTool(t, cs, "alpha", nil)
	body := onlyBody(t, project)
	if _, ok := body.Features[agentcat.ServerInfoKeyPrompts]; ok {
		t.Error("features has prompts, want the failed list left out")
	}
	if got := len(featureItems(t, body, agentcat.ServerInfoKeyTools)); got != 4 {
		t.Errorf("tools = %d, want 4", got)
	}
}

func TestServerInfo_V5_FailedToolListSendsNothing(t *testing.T) {
	agedHeartbeatMemory(t)
	project := projectID("v5")
	s := newDemoServer(nil)
	s.AddReceivingMiddleware(failList("tools/list"))
	cs, _ := trackDemo(t, s, project, nil)

	if res := callTool(t, cs, "alpha", nil); res.IsError {
		t.Fatalf("tool call failed: %+v", res)
	}
	expectPosts(t, project, 0)
}

func TestServerInfo_V7_OnePostAnHour(t *testing.T) {
	clock := agedHeartbeatMemory(t)
	project := projectID("v7")
	cs, _ := trackDemo(t, newDemoServer(nil), project, nil)

	for i := 0; i < 3; i++ {
		callTool(t, cs, "alpha", nil)
	}
	expectPosts(t, project, 1)

	clock.advance(3599 * time.Second)
	callTool(t, cs, "alpha", nil)
	expectPosts(t, project, 1)

	clock.advance(time.Second)
	callTool(t, cs, "alpha", nil)
	expectPosts(t, project, 2)
}

func TestServerInfo_V8_TwoServersInOneProcess(t *testing.T) {
	agedHeartbeatMemory(t)
	project := projectID("v8")
	first, _ := trackDemo(t, newDemoServer(nil), project, nil)
	second, _ := trackDemo(t, newDemoServer(nil), project, nil)

	callTool(t, first, "alpha", nil)
	callTool(t, second, "alpha", nil)
	expectPosts(t, project, 1)

	other := mcp.NewServer(&mcp.Implementation{Name: "other-server", Version: "1"}, nil)
	other.AddTool(&mcp.Tool{Name: "alpha", InputSchema: objectSchema(nil)}, textTool("a"))
	third, _ := trackDemo(t, other, project, nil)
	callTool(t, third, "alpha", nil)
	expectPosts(t, project, 2)
}

func TestServerInfo_V9_NoProjectOrTracingOff(t *testing.T) {
	agedHeartbeatMemory(t)
	noProject, _ := trackDemo(t, newDemoServer(nil), "", nil)
	callTool(t, noProject, "alpha", nil)
	if _, err := noProject.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	expectPosts(t, "", 0)

	project := projectID("v9")
	off, _ := trackDemo(t, newDemoServer(nil), project, &Options{DisableTracing: true})
	callTool(t, off, "alpha", nil)
	if _, err := off.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	expectPosts(t, project, 0)
}

// captureOutput swaps standard output and standard error for pipes and
// returns a function that restores them and returns what was written.
func captureOutput(t *testing.T) func() string {
	t.Helper()
	stdout, stderr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = w, w
	out := make(chan string, 1)
	go func() {
		raw, _ := io.ReadAll(r)
		out <- string(raw)
	}()
	return func() string {
		os.Stdout, os.Stderr = stdout, stderr
		_ = w.Close()
		return <-out
	}
}

func TestServerInfo_V10_FailuresNeverReachTheToolCall(t *testing.T) {
	hangOn := func(method string, release <-chan struct{}) mcp.Middleware {
		return func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, m string, req mcp.Request) (mcp.Result, error) {
				if m == method {
					<-release
				}
				return next(ctx, m, req)
			}
		}
	}
	panicOn := func(method string) mcp.Middleware {
		return func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, m string, req mcp.Request) (mcp.Result, error) {
				if m == method {
					panic(method + " exploded")
				}
				return next(ctx, m, req)
			}
		}
	}

	cases := []struct {
		name  string
		setup func(s *mcp.Server, opts *Options, release <-chan struct{})
		// answer replaces the recorder's 202; posts is the POST count once the
		// heartbeat has run, or -1 when the heartbeat is still held.
		answer func(release <-chan struct{}) func(context.Context) (int, error)
		posts  int
	}{
		{name: "api never answers", posts: -1, answer: func(release <-chan struct{}) func(context.Context) (int, error) {
			return func(context.Context) (int, error) {
				<-release
				return 0, errors.New("no answer")
			}
		}},
		{name: "list handler fails", posts: 1, setup: func(s *mcp.Server, _ *Options, _ <-chan struct{}) {
			s.AddReceivingMiddleware(panicOn("resources/list"))
		}},
		{name: "list handler never returns", posts: -1, setup: func(s *mcp.Server, _ *Options, release <-chan struct{}) {
			s.AddReceivingMiddleware(hangOn("prompts/list", release))
		}},
		{name: "redaction function fails", posts: 0, setup: func(_ *mcp.Server, opts *Options, _ <-chan struct{}) {
			opts.RedactSensitiveInformation = func(string) string { panic("redaction exploded") }
		}},
		{name: "api answers 500", posts: 1, answer: func(<-chan struct{}) func(context.Context) (int, error) {
			return func(context.Context) (int, error) {
				return http.StatusInternalServerError, errors.New("500")
			}
		}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agedHeartbeatMemory(t)
			release := make(chan struct{})
			t.Cleanup(func() {
				close(release)
				waitHeartbeats(t)
			})
			if tc.answer != nil {
				heartbeats.answerWith(t, tc.answer(release))
			}
			project := projectID("v10" + string(rune('a'+i)))
			s := newDemoServer(nil)
			opts := DefaultOptions()
			if tc.setup != nil {
				tc.setup(s, opts, release)
			}
			cs, _ := trackDemo(t, s, project, opts)

			restore := captureOutput(t)
			start := time.Now()
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "alpha"})
			elapsed := time.Since(start)
			output := restore()

			if err != nil || res.IsError || lastText(t, res) != "alpha done" {
				t.Fatalf("tool call: err %v, result %+v", err, res)
			}
			if elapsed > 2*time.Second {
				t.Errorf("tool call took %v, want it not to wait for the heartbeat", elapsed)
			}
			if output != "" {
				t.Errorf("output on stdout or stderr: %q", output)
			}
			if tc.posts >= 0 {
				expectPosts(t, project, tc.posts)
			}
		})
	}
}

func replaceSecret(s string) string { return strings.ReplaceAll(s, "secret", "[x]") }

func TestServerInfo_V11_RedactionCoversTextNotNames(t *testing.T) {
	agedHeartbeatMemory(t)
	project := projectID("v11")
	s := mcp.NewServer(&mcp.Implementation{Name: "secret-server", Version: "1"},
		&mcp.ServerOptions{Instructions: "the secret plan"})
	s.AddTool(&mcp.Tool{
		Name:        "secret_tool",
		Description: "secret tool",
		InputSchema: objectSchema(map[string]any{"q": map[string]any{"type": "string", "description": "a secret"}}),
	}, textTool("done"))
	s.AddResource(&mcp.Resource{URI: "file:///secret.txt", Name: "secret-file"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{}, nil
		})
	cs, _ := trackDemo(t, s, project, &Options{RedactSensitiveInformation: replaceSecret})

	callTool(t, cs, "secret_tool", nil)
	body := onlyBody(t, project)
	if body.GetInstructions() != "the [x] plan" {
		t.Errorf("instructions = %q", body.GetInstructions())
	}
	if body.GetServerName() != "secret-server" {
		t.Errorf("server_name = %q, want it unredacted", body.GetServerName())
	}
	var tool map[string]any
	for _, item := range featureItems(t, body, agentcat.ServerInfoKeyTools) {
		if item["name"] == "secret_tool" {
			tool = item
		}
	}
	if tool == nil {
		t.Fatal("tool secret_tool is missing or its name was redacted")
	}
	if tool["description"] != "[x] tool" {
		t.Errorf("description = %v", tool["description"])
	}
	q := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)["q"].(map[string]any)
	if q["description"] != "a [x]" {
		t.Errorf("schema description = %v", q["description"])
	}
	resource := featureItems(t, body, agentcat.ServerInfoKeyResources)[0]
	if resource["uri"] != "file:///secret.txt" {
		t.Errorf("uri = %v, want it unredacted", resource["uri"])
	}
}

func TestServerInfo_V12_FailedRedactionSendsNothing(t *testing.T) {
	agedHeartbeatMemory(t)
	project := projectID("v12")
	cs, _ := trackDemo(t, newDemoServer(nil), project, &Options{
		RedactSensitiveInformation: func(string) string { panic("redaction exploded") },
	})
	callTool(t, cs, "alpha", nil)
	expectPosts(t, project, 0)
}

func TestServerInfo_V12_UsesTheRedactionOfItsOwnTrackCall(t *testing.T) {
	agedHeartbeatMemory(t)
	first := projectID("v12a")
	second := projectID("v12b")
	csA, _ := trackDemo(t, newDemoServer(nil), first, &Options{RedactSensitiveInformation: func(s string) string { return "A" }})
	csB, _ := trackDemo(t, newDemoServer(nil), second, &Options{RedactSensitiveInformation: func(s string) string { return "B" }})
	callTool(t, csA, "alpha", nil)
	callTool(t, csB, "alpha", nil)
	if got := onlyBody(t, first).GetInstructions(); got != "A" {
		t.Errorf("first server instructions = %q, want A", got)
	}
	if got := onlyBody(t, second).GetInstructions(); got != "B" {
		t.Errorf("second server instructions = %q, want B", got)
	}
}

func TestServerInfo_V15_SameServerNameAndVersionAsTheEvent(t *testing.T) {
	agedHeartbeatMemory(t)
	project := projectID("v15")
	cs, mock := trackDemo(t, newDemoServer(nil), project, nil)

	callTool(t, cs, "alpha", nil)
	events := waitForEventType(mock, "mcp:tools/call", 1, 3*time.Second)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	body := onlyBody(t, project)
	if body.GetServerName() != events[0].GetServerName() || body.GetServerVersion() != events[0].GetServerVersion() {
		t.Errorf("heartbeat (%q, %q) != event (%q, %q)", body.GetServerName(), body.GetServerVersion(),
			events[0].GetServerName(), events[0].GetServerVersion())
	}
}

func TestServerInfo_V16_SuiteSendsHeartbeatsToTheRecorder(t *testing.T) {
	newHeartbeatMemory(t)
	project := projectID("v16")
	s := newDemoServer(nil)
	shutdown, err := Track(s, project, &Options{})
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	t.Cleanup(func() {
		agentcat.UnregisterServer(s)
		_ = shutdown(context.Background())
	})
	cs := connectClient(t, s)
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	// The recorder installed by TestMain got the heartbeat of a server that
	// Track set up with the default API URL: it went nowhere else.
	expectPosts(t, project, 1)
}

func TestServerInfo_V17_NewMemorySendsForListAndFeedbackOnly(t *testing.T) {
	newHeartbeatMemory(t)
	project := projectID("v17")
	cs, _ := trackDemo(t, newDemoServer(nil), project, nil)

	callTool(t, cs, "alpha", nil)
	expectPosts(t, project, 0)
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	expectPosts(t, project, 1)

	agentcat.ResetServerInfoForTest()
	callTool(t, cs, agentcat.GetMoreToolsName, map[string]any{"context": "need a delete tool"})
	expectPosts(t, project, 2)
}

func TestServerInfo_V18_NoMemoryBetweenRequests(t *testing.T) {
	newHeartbeatMemory(t)
	project := projectID("v18")
	cs, _ := trackDemo(t, newDemoServer(nil), project, nil)

	for i := 0; i < 3; i++ {
		agentcat.ResetServerInfoForTest()
		if res := callTool(t, cs, "alpha", nil); lastText(t, res) != "alpha done" {
			t.Fatalf("tool call %d: %+v", i, res)
		}
		waitHeartbeats(t)
		agentcat.ResetServerInfoForTest()
		if tools := listTools(t, cs); len(tools) != 4 {
			t.Fatalf("tools/list %d: %d tools", i, len(tools))
		}
		waitHeartbeats(t)
	}
	expectPosts(t, project, 3)
}

func TestServerInfo_V19_ToolCallWaitsForAMemoryOf60Seconds(t *testing.T) {
	clock := newHeartbeatMemory(t)
	project := projectID("v19")
	cs, _ := trackDemo(t, newDemoServer(nil), project, nil)

	callTool(t, cs, "alpha", nil)
	expectPosts(t, project, 0)
	clock.advance(59 * time.Second)
	callTool(t, cs, "alpha", nil)
	expectPosts(t, project, 0)
	clock.advance(time.Second)
	callTool(t, cs, "alpha", nil)
	expectPosts(t, project, 1)
}

// TestGetServerOptions_ReadsPrivateOptsField is the change-detection guard
// for the read of the go-sdk's private opts field, which holds the
// instructions.
func TestGetServerOptions_ReadsPrivateOptsField(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "1"},
		&mcp.ServerOptions{Instructions: "probe instructions", PageSize: 7})
	got := getServerOptions(s)
	if got == nil {
		t.Fatal("getServerOptions returned nil — did the go-sdk change its private opts field?")
	}
	if got.Instructions != "probe instructions" || got.PageSize != 7 {
		t.Errorf("getServerOptions = %+v, want the NewServer options", got)
	}
}

func TestGetServerImpl_ReadsTitle(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "1", Title: "Probe Title"}, nil)
	if got := getServerImpl(s); got == nil || got.Title != "Probe Title" {
		t.Fatalf("getServerImpl = %+v, want the title", got)
	}
}
