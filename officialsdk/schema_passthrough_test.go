package officialsdk

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// echoingToolHandler echoes the raw arguments the handler received, so tests
// can observe exactly what survived stripping.
func echoingToolHandler(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	echoed := "{}"
	if len(req.Params.Arguments) > 0 {
		echoed = string(req.Params.Arguments)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: echoed}}}, nil
}

// trackServer installs the tracking middleware on an already-built server and
// connects a real client, exactly as createTestServer does for the fixture
// server. Returns the client session and the mock publisher.
func trackServer(t *testing.T, server *mcp.Server, opts *Options) (*mcp.ClientSession, *mockPublisher) {
	t.Helper()

	if opts == nil {
		opts = DefaultOptions()
	}
	coreOpts := &agentcat.Options{
		DisableReportMissing:   opts.DisableReportMissing,
		DisableToolCallContext: opts.DisableToolCallContext,
		DisableTracing:         opts.DisableTracing,
		EnableAgentTracking:    opts.EnableAgentTracking,
	}
	agentcat.RegisterServer(server, &agentcat.AgentCatInstance{ProjectID: "proj_test", Options: coreOpts})

	mock := &mockPublisher{}
	middleware := newTrackingMiddleware(server, "proj_test", opts, mock.publish,
		&mcp.Implementation{Name: "passthrough-test-server", Version: "1.0.0"})
	server.AddReceivingMiddleware(middleware)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "passthrough-client", Version: "0.1.0"}, nil)
	pinLegacyInitializeHandshake(client)
	clientSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		clientSession.Close()
		serverSession.Wait()
		agentcat.UnregisterServer(server)
	})
	return clientSession, mock
}

// decodeEchoed decodes the arguments an echoing tool reported.
func decodeEchoed(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("echo tool returned no content")
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("echo tool content is %T, want *mcp.TextContent", result.Content[0])
	}
	var echoed map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &echoed); err != nil {
		t.Fatalf("decode echoed args %q: %v", tc.Text, err)
	}
	return echoed
}

// TestUnparseableSchemaIsAdvertisedUntouched pins that a schema this SDK
// cannot read keeps its own contract: it must never be treated as "declares
// nothing" and replaced with something AgentCat synthesised.
//
// `true` is a legal JSON Schema meaning "accept anything". The go-sdk's
// AddTool refuses it for the INPUT half (it panics unless the input schema is
// an object with type "object"), so the reachable case here is the output
// half — the input half is covered by the mcpgo suite, whose library accepts
// a raw input schema of any shape.
func TestUnparseableSchemaIsAdvertisedUntouched(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "opaque-schema-server", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:         "opaque_output",
		Description:  "Declares an output schema this SDK cannot read",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`true`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"ok": true}}, nil
	})

	clientSession, _ := trackServer(t, server, DefaultOptions())

	listed, err := clientSession.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "opaque_output" {
			continue
		}
		raw, err := json.Marshal(tool.OutputSchema)
		if err != nil {
			t.Fatalf("marshal advertised output schema: %v", err)
		}
		if string(raw) != "true" {
			t.Errorf("unreadable output schema must be advertised verbatim, got %s", raw)
		}
	}

	// The mirror must stay off for a schema the SDK could not read.
	res, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "opaque_output",
		Arguments: map[string]any{"session_id": sid("o")},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if sc, ok := res.StructuredContent.(map[string]any); ok {
		if _, has := sc["_mcp_instructions"]; has {
			t.Errorf("a schema the SDK cannot read must not be mirrored into: %v", sc)
		}
	}
}

// TestRegistriesMergeAcrossPaginatedLists is the regression gate for
// last-write-wins registries: with ServerOptions.PageSize set, a tools/list
// describes one page, and the tools on every other page must keep the entries
// their own page recorded — otherwise a page-one tool receives AgentCat's
// injected arguments in the customer's handler.
func TestRegistriesMergeAcrossPaginatedLists(t *testing.T) {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "paginated-server", Version: "1.0.0"},
		&mcp.ServerOptions{PageSize: 1},
	)
	for _, name := range []string{"a_first", "b_second"} {
		server.AddTool(&mcp.Tool{
			Name:        name,
			Description: "Echoes the arguments the handler received",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"p":{"type":"string"}}}`),
		}, echoingToolHandler)
	}

	clientSession, _ := trackServer(t, server, DefaultOptions())
	ctx := context.Background()

	page1, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools(page 1): %v", err)
	}
	if len(page1.Tools) != 1 || page1.Tools[0].Name != "a_first" {
		t.Fatalf("expected page 1 to hold only a_first, got %d tools", len(page1.Tools))
	}
	if page1.NextCursor == "" {
		t.Fatal("expected a next cursor after page 1")
	}
	page2, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("ListTools(page 2): %v", err)
	}
	if len(page2.Tools) != 1 || page2.Tools[0].Name != "b_second" {
		t.Fatalf("expected page 2 to hold only b_second, got %d tools", len(page2.Tools))
	}

	res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "a_first",
		Arguments: map[string]any{"p": "keep", "session_id": sid("abc"), "context": "why"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	echoed := decodeEchoed(t, res)
	for _, name := range []string{"session_id", "context"} {
		if _, has := echoed[name]; has {
			t.Errorf("page-1 tool stopped stripping %s after page 2 was listed: %v", name, echoed)
		}
	}
	if echoed["p"] != "keep" {
		t.Errorf("real argument lost: %v", echoed)
	}
}

// TestTrackIsIdempotentPerServer pins that a second Track on the same server
// installs nothing: two receiving middlewares would decorate the wire twice,
// publish two events per call, and — because the second one's rebuild stash
// captures the FIRST AgentCat middleware as its inner handler — stop argument
// stripping entirely.
func TestTrackIsIdempotentPerServer(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "double-track-server", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "echo_args",
		Description: "Echoes the arguments the handler received",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"p":{"type":"string"}}}`),
	}, echoingToolHandler)

	t.Cleanup(func() { agentcat.UnregisterServer(server) })

	if _, err := Track(server, "proj_test", &Options{DisableReportMissing: true}); err != nil {
		t.Fatalf("first Track: %v", err)
	}
	if _, err := Track(server, "proj_other", &Options{DisableReportMissing: true}); err != nil {
		t.Fatalf("second Track: %v", err)
	}

	if got := agentcat.GetInstance(server).ProjectID; got != "proj_test" {
		t.Errorf("the first Track's configuration must stand, got project %q", got)
	}

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "double-track-client", Version: "0.1.0"}, nil)
	pinLegacyInitializeHandshake(client)
	clientSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() {
		clientSession.Close()
		serverSession.Wait()
	}()

	if _, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{}); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo_args",
		Arguments: map[string]any{"p": "keep", "context": "why"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	echoed := decodeEchoed(t, res)
	if _, has := echoed["context"]; has {
		t.Errorf("a second Track broke argument stripping: %v", echoed)
	}

	// Exactly one mint-back block on the wire, not two.
	mintBacks := 0
	for _, block := range res.Content {
		if tc, ok := block.(*mcp.TextContent); ok && strings.Contains(tc.Text, "[MCP INSTRUCTIONS]") {
			mintBacks++
		}
	}
	if mintBacks != 1 {
		t.Errorf("expected exactly 1 mint-back block on the wire, got %d: %v", mintBacks, res.Content)
	}
}

// TestCaptureIsSynchronousAndNeverDropsEvents is the regression gate for
// adapter-side event loss.
//
// Capture used to run on a detached goroutine behind a 100-slot semaphore
// whose full arm DISCARDED the event. The slot lifetime is dominated by
// customer callbacks (Identify, EventTags, EventProperties) that the docs only
// ASK to be cheap, so a slow one plus enough concurrency silently lost
// analytics data — in an SDK whose entire job is not to lose it.
//
// Capture is synchronous now, so the only bounded queue left is the
// publisher's, which is the designed backpressure point and warns when it
// overflows. This drives MORE concurrent calls than that old bound, each held
// open by a slow Identify, and requires every single one to publish.
func TestCaptureIsSynchronousAndNeverDropsEvents(t *testing.T) {
	// Comfortably above the old 100-slot semaphore bound.
	const calls = 130

	server := mcp.NewServer(&mcp.Implementation{Name: "no-drop-server", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "noop",
		Description: "Does nothing",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	var identifyCalls atomic.Int64
	opts := &Options{
		DisableReportMissing: true,
		Identify: func(ctx context.Context, req mcp.Request) *UserIdentity {
			identifyCalls.Add(1)
			// Slow enough that every capture overlaps the others.
			time.Sleep(25 * time.Millisecond)
			return &UserIdentity{UserID: "u1"}
		},
	}
	clientSession, mock := trackServer(t, server, opts)

	var wg sync.WaitGroup
	wg.Add(calls)
	for i := 0; i < calls; i++ {
		go func() {
			defer wg.Done()
			if _, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "noop"}); err != nil {
				t.Errorf("CallTool: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := int(identifyCalls.Load()); got != calls {
		t.Errorf("Identify ran %d times, want %d — some captures never started", got, calls)
	}
	// Synchronous capture means every event is already published by the time
	// its own call returned; no settle window, no polling.
	if got := len(mock.getEvents()); got != calls {
		t.Errorf("published %d events for %d concurrent calls; the adapter must not drop any", got, calls)
	}
}

// TestCaptureCompletesBeforeTheCallReturns pins the synchronicity itself: the
// event is published by the time CallTool returns, with nothing to wait for.
// That is what makes Shutdown safe without draining anything.
func TestCaptureCompletesBeforeTheCallReturns(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "sync-capture-server", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "noop",
		Description: "Does nothing",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	clientSession, mock := trackServer(t, server, &Options{DisableReportMissing: true})

	if _, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "noop"}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got := len(mock.getEvents()); got != 1 {
		t.Errorf("expected the event to be published before CallTool returned, got %d events", got)
	}
}

// TestSlowIdentifyDoesNotBreakTheCall pins the cost side of the trade: a slow
// customer callback charges the tool call latency, but must never fail it or
// lose its event.
func TestSlowIdentifyDoesNotBreakTheCall(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "slow-identify-server", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "noop",
		Description: "Does nothing",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	opts := &Options{
		DisableReportMissing: true,
		Identify: func(ctx context.Context, req mcp.Request) *UserIdentity {
			time.Sleep(60 * time.Millisecond)
			return &UserIdentity{UserID: "slow"}
		},
	}
	clientSession, mock := trackServer(t, server, opts)

	start := time.Now()
	result, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{Name: "noop"})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Errorf("a slow Identify must not fail the call: %v", result.Content)
	}
	if elapsed < 50*time.Millisecond {
		t.Errorf("expected the slow callback to be charged to the call (%v elapsed) — capture is supposed to be synchronous", elapsed)
	}
	if got := len(mock.getEvents()); got != 1 {
		t.Errorf("expected 1 published event, got %d", got)
	}
}
