package mcpgo

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

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

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

// agedHeartbeatMemory is newHeartbeatMemory with a memory 60 seconds old: one
// check that cannot start a heartbeat starts the memory, then the clock moves.
func agedHeartbeatMemory(t *testing.T) *fakeClock {
	t.Helper()
	clock := newHeartbeatMemory(t)
	agentcat.MaybeStartServerInfo(context.Background(), agentcat.ServerInfoTriggerToolsList,
		&agentcat.AgentCatInstance{}, "", nil)
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

// ── fixture server ──────────────────────────────────────────────────────────

var longDescription = strings.Repeat("The beta tool does one careful thing. ", 60)[:2000]

func textHandler(text string) server.ToolHandlerFunc {
	return func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(text), nil
	}
}

// newDemoServer builds the fixture server: three tools, two resources, one
// resource template and two prompts.
func newDemoServer(opts ...server.ServerOption) *server.MCPServer {
	opts = append([]server.ServerOption{
		server.WithTitle("Demo Title"),
		server.WithInstructions("Use the demo tools."),
	}, opts...)
	s := server.NewMCPServer("demo-server", "1.2.3", opts...)
	s.AddTool(mcp.NewTool("alpha", mcp.WithDescription("First tool")), textHandler("alpha done"))
	s.AddTool(mcp.NewTool("beta",
		mcp.WithToolTitle("Beta Tool"),
		mcp.WithDescription(longDescription),
		mcp.WithString("query", mcp.Description("What to look for")),
		mcp.WithRawOutputSchema(json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`)),
		mcp.WithReadOnlyHintAnnotation(true),
	), textHandler("beta done"))
	s.AddTool(mcp.NewTool("gamma", mcp.WithDescription("Third tool")), textHandler("gamma done"))

	read := func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) { return nil, nil }
	s.AddResource(mcp.NewResource("file:///readme.txt", "readme", mcp.WithResourceDescription("The readme")), read)
	s.AddResource(mcp.NewResource("file:///notes.txt", "notes", mcp.WithMIMEType("text/plain")), read)
	s.AddResourceTemplate(mcp.NewResourceTemplate("file:///docs/{id}", "doc"), read)

	prompt := func(context.Context, mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{}, nil
	}
	s.AddPrompt(mcp.NewPrompt("greet", mcp.WithPromptDescription("Say hello"), mcp.WithArgument("who", mcp.RequiredArgument())), prompt)
	s.AddPrompt(mcp.NewPrompt("plain", mcp.WithPromptDescription("No arguments")), prompt)
	return s
}

// trackDemo tracks s the way Track does, with events going to a mock
// publisher, and connects an initialized in-process client.
func trackDemo(t *testing.T, s *server.MCPServer, project string, opts *Options) (*client.Client, *mockPublisher) {
	t.Helper()
	if opts == nil {
		opts = DefaultOptions()
	}
	instance := newTestInstance(s, project, opts)
	instance.Options.RedactSensitiveInformation = opts.RedactSensitiveInformation
	agentcat.RegisterServer(s, instance)
	mock := &mockPublisher{}
	installTracking(s, instance, opts, mock.publish)

	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "heartbeat-test-client", Version: "0.1.0"}
	if _, err := c.Initialize(ctx, init); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c.Close()
		unregisterServer(s)
	})
	return c, mock
}

func callTool(t *testing.T, c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

func listAllTools(t *testing.T, c *client.Client) []mcp.Tool {
	t.Helper()
	var tools []mcp.Tool
	req := mcp.ListToolsRequest{}
	for {
		res, err := c.ListTools(context.Background(), req)
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		tools = append(tools, res.Tools...)
		if res.NextCursor == "" {
			return tools
		}
		req.Params.Cursor = res.NextCursor
	}
}

// wireTools returns the tools/list result as it goes on the wire, keyed by
// tool name. A client's decoded mcp.Tool re-marshals with fields the wire did
// not carry, so the comparison reads the wire form.
func wireTools(t *testing.T, s *server.MCPServer) map[string]any {
	t.Helper()
	answer := s.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	reply, ok := plainJSON(t, answer).(map[string]any)["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list answered %v", answer)
	}
	tools := map[string]any{}
	for _, tool := range reply["tools"].([]any) {
		tools[tool.(map[string]any)["name"].(string)] = tool
	}
	return tools
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

// ── the test table ──────────────────────────────────────────────────────────

func TestServerInfo_V1_OneToolCallSendsWhatAClientSees(t *testing.T) {
	agedHeartbeatMemory(t)
	project := projectID("v1")
	s := newDemoServer()
	c, _ := trackDemo(t, s, project, nil)

	callTool(t, c, "alpha", nil)
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

	want := wireTools(t, s)
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
	s := server.NewMCPServer("paged", "1", server.WithPaginationLimit(2))
	for _, name := range []string{"t1", "t2", "t3", "t4"} {
		s.AddTool(mcp.NewTool(name), textHandler(name))
	}
	c, _ := trackDemo(t, s, project, nil)

	res, err := c.ListToolsByPage(context.Background(), mcp.ListToolsRequest{})
	if err != nil || res.NextCursor == "" {
		t.Fatalf("first page: err %v; want a paged list", err)
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
	s := server.NewMCPServer("tools-only", "1")
	s.AddTool(mcp.NewTool("alpha"), textHandler("a"))
	c, _ := trackDemo(t, s, project, nil)

	callTool(t, c, "alpha", nil)
	if got := featureKeys(onlyBody(t, project)); !reflect.DeepEqual(got, []string{"tools"}) {
		t.Fatalf("features keys = %v, want [tools]", got)
	}
}

func TestServerInfo_V4_FailedPromptListIsLeftOut(t *testing.T) {
	agedHeartbeatMemory(t)
	project := projectID("v4")
	hooks := &server.Hooks{}
	hooks.AddBeforeListPrompts(func(context.Context, any, *mcp.ListPromptsRequest) {
		panic("prompt handler failed")
	})
	c, _ := trackDemo(t, newDemoServer(server.WithHooks(hooks)), project, nil)

	callTool(t, c, "alpha", nil)
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
	hooks := &server.Hooks{}
	hooks.AddBeforeListTools(func(context.Context, any, *mcp.ListToolsRequest) {
		panic("tool handler failed")
	})
	c, _ := trackDemo(t, newDemoServer(server.WithHooks(hooks)), project, nil)

	if res := callTool(t, c, "alpha", nil); res.IsError || resultText(res) != "alpha done" {
		t.Fatalf("tool call: %+v", res)
	}
	expectPosts(t, project, 0)
}

func TestServerInfo_V7_OnePostAnHour(t *testing.T) {
	clock := agedHeartbeatMemory(t)
	project := projectID("v7")
	c, _ := trackDemo(t, newDemoServer(), project, nil)

	for i := 0; i < 3; i++ {
		callTool(t, c, "alpha", nil)
	}
	expectPosts(t, project, 1)

	clock.advance(3599 * time.Second)
	callTool(t, c, "alpha", nil)
	expectPosts(t, project, 1)

	clock.advance(time.Second)
	callTool(t, c, "alpha", nil)
	expectPosts(t, project, 2)
}

func TestServerInfo_V8_TwoServersInOneProcess(t *testing.T) {
	agedHeartbeatMemory(t)
	project := projectID("v8")
	first, _ := trackDemo(t, newDemoServer(), project, nil)
	second, _ := trackDemo(t, newDemoServer(), project, nil)

	callTool(t, first, "alpha", nil)
	callTool(t, second, "alpha", nil)
	expectPosts(t, project, 1)

	other := server.NewMCPServer("other-server", "1")
	other.AddTool(mcp.NewTool("alpha"), textHandler("a"))
	third, _ := trackDemo(t, other, project, nil)
	callTool(t, third, "alpha", nil)
	expectPosts(t, project, 2)
}

func TestServerInfo_V9_NoProjectOrTracingOff(t *testing.T) {
	agedHeartbeatMemory(t)
	noProject, _ := trackDemo(t, newDemoServer(), "", nil)
	callTool(t, noProject, "alpha", nil)
	listAllTools(t, noProject)
	expectPosts(t, "", 0)

	project := projectID("v9")
	off, _ := trackDemo(t, newDemoServer(), project, &Options{DisableTracing: true})
	callTool(t, off, "alpha", nil)
	listAllTools(t, off)
	expectPosts(t, project, 0)
}

// captureOutput swaps standard output and standard error for a pipe and
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
	cases := []struct {
		name   string
		hooks  func(h *server.Hooks, release <-chan struct{})
		redact func(string) string
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
		{name: "list handler fails", posts: 1, hooks: func(h *server.Hooks, _ <-chan struct{}) {
			h.AddBeforeListResources(func(context.Context, any, *mcp.ListResourcesRequest) {
				panic("resource handler exploded")
			})
		}},
		{name: "list handler never returns", posts: -1, hooks: func(h *server.Hooks, release <-chan struct{}) {
			h.AddBeforeListPrompts(func(context.Context, any, *mcp.ListPromptsRequest) { <-release })
		}},
		{name: "redaction function fails", posts: 0, redact: func(string) string { panic("redaction exploded") }},
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
			hooks := &server.Hooks{}
			if tc.hooks != nil {
				tc.hooks(hooks, release)
			}
			project := projectID("v10" + string(rune('a'+i)))
			c, _ := trackDemo(t, newDemoServer(server.WithHooks(hooks)), project,
				&Options{RedactSensitiveInformation: tc.redact})

			restore := captureOutput(t)
			start := time.Now()
			req := mcp.CallToolRequest{}
			req.Params.Name = "alpha"
			res, err := c.CallTool(context.Background(), req)
			elapsed := time.Since(start)
			output := restore()

			if err != nil || res.IsError || resultText(res) != "alpha done" {
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
	s := server.NewMCPServer("secret-server", "1", server.WithInstructions("the secret plan"))
	s.AddTool(mcp.NewTool("secret_tool",
		mcp.WithDescription("secret tool"),
		mcp.WithString("q", mcp.Description("a secret")),
	), textHandler("done"))
	s.AddResource(mcp.NewResource("file:///secret.txt", "secret-file"),
		func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) { return nil, nil })
	c, _ := trackDemo(t, s, project, &Options{RedactSensitiveInformation: replaceSecret})

	callTool(t, c, "secret_tool", nil)
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
	c, _ := trackDemo(t, newDemoServer(), project, &Options{
		RedactSensitiveInformation: func(string) string { panic("redaction exploded") },
	})
	callTool(t, c, "alpha", nil)
	expectPosts(t, project, 0)
}

func TestServerInfo_V12_UsesTheRedactionOfItsOwnTrackCall(t *testing.T) {
	agedHeartbeatMemory(t)
	first := projectID("v12a")
	second := projectID("v12b")
	cA, _ := trackDemo(t, newDemoServer(), first, &Options{RedactSensitiveInformation: func(string) string { return "A" }})
	cB, _ := trackDemo(t, newDemoServer(), second, &Options{RedactSensitiveInformation: func(string) string { return "B" }})
	callTool(t, cA, "alpha", nil)
	callTool(t, cB, "alpha", nil)
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
	c, mock := trackDemo(t, newDemoServer(), project, nil)

	callTool(t, c, "alpha", nil)
	events := filterEvents(mock.waitForEvents(1, 3*time.Second), "mcp:tools/call")
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
	s := newDemoServer()
	shutdown, err := Track(s, project, &Options{})
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	t.Cleanup(func() {
		unregisterServer(s)
		_ = shutdown(context.Background())
	})
	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	if _, err := c.Initialize(context.Background(), init); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListTools(context.Background(), mcp.ListToolsRequest{}); err != nil {
		t.Fatal(err)
	}
	// The recorder installed by TestMain got the heartbeat of a server that
	// Track set up with the default API URL: it went nowhere else.
	expectPosts(t, project, 1)
}

func TestServerInfo_V17_NewMemorySendsForListAndFeedbackOnly(t *testing.T) {
	newHeartbeatMemory(t)
	project := projectID("v17")
	c, _ := trackDemo(t, newDemoServer(), project, nil)

	callTool(t, c, "alpha", nil)
	expectPosts(t, project, 0)
	listAllTools(t, c)
	expectPosts(t, project, 1)

	agentcat.ResetServerInfoForTest()
	callTool(t, c, agentcat.GetMoreToolsName, map[string]any{"context": "need a delete tool"})
	expectPosts(t, project, 2)
}

func TestServerInfo_V18_NoMemoryBetweenRequests(t *testing.T) {
	newHeartbeatMemory(t)
	project := projectID("v18")
	c, _ := trackDemo(t, newDemoServer(), project, nil)

	for i := 0; i < 3; i++ {
		agentcat.ResetServerInfoForTest()
		if res := callTool(t, c, "alpha", nil); resultText(res) != "alpha done" {
			t.Fatalf("tool call %d: %+v", i, res)
		}
		waitHeartbeats(t)
		agentcat.ResetServerInfoForTest()
		if tools := listAllTools(t, c); len(tools) != 4 {
			t.Fatalf("tools/list %d: %d tools", i, len(tools))
		}
		waitHeartbeats(t)
	}
	expectPosts(t, project, 3)
}

// TestServerInfo_V18_RegistryRebuildIsNotAListRequest pins that the synthetic
// tools/list a tool call drives on an instance that never served a list does
// not count as a list request: in a memory of age zero, a tool call on a
// fresh server sends nothing.
func TestServerInfo_V18_RegistryRebuildIsNotAListRequest(t *testing.T) {
	newHeartbeatMemory(t)
	project := projectID("v18r")
	for i := 0; i < 3; i++ {
		agentcat.ResetServerInfoForTest()
		s := newDemoServer()
		instance := newTestInstance(s, project, DefaultOptions())
		agentcat.RegisterServer(s, instance)
		installTracking(s, instance, DefaultOptions(), (&mockPublisher{}).publish)
		if res := callToolRaw(t, s, "alpha", nil); resultText(res) != "alpha done" {
			t.Fatalf("tool call %d: %+v", i, res)
		}
		if instance.Registries.Load() == nil {
			t.Fatal("the tool call did not rebuild the registries")
		}
		unregisterServer(s)
	}
	expectPosts(t, project, 0)
}

func TestServerInfo_V19_ToolCallWaitsForAMemoryOf60Seconds(t *testing.T) {
	clock := newHeartbeatMemory(t)
	project := projectID("v19")
	c, _ := trackDemo(t, newDemoServer(), project, nil)

	callTool(t, c, "alpha", nil)
	expectPosts(t, project, 0)
	clock.advance(59 * time.Second)
	callTool(t, c, "alpha", nil)
	expectPosts(t, project, 0)
	clock.advance(time.Second)
	callTool(t, c, "alpha", nil)
	expectPosts(t, project, 1)
}

// TestGetServerTitleAndInstructions_ReadsPrivateFields is the change-detection
// tripwire for the reflective read of mcp-go's unexported implementation and
// instructions fields.
func TestGetServerTitleAndInstructions_ReadsPrivateFields(t *testing.T) {
	s := server.NewMCPServer("probe", "1", server.WithTitle("Probe Title"), server.WithInstructions("probe instructions"))
	title, instructions := getServerTitleAndInstructions(s)
	if title != "Probe Title" || instructions != "probe instructions" {
		t.Errorf("getServerTitleAndInstructions = (%q, %q) — mcp-go's private fields may have been renamed", title, instructions)
	}
}
