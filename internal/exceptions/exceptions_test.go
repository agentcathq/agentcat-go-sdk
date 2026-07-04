package exceptions

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

// --- Test helpers ---

// customError is a typed error for testing type extraction.
type customError struct {
	Code int
	Msg  string
}

func (e *customError) Error() string { return fmt.Sprintf("code %d: %s", e.Code, e.Msg) }

// wrappedError wraps another error for testing chain unwrapping.
type wrappedError struct {
	msg   string
	inner error
}

func (e *wrappedError) Error() string { return e.msg }
func (e *wrappedError) Unwrap() error { return e.inner }

// stackError implements the stackTracer interface for testing.
type stackError struct {
	msg string
	pcs []uintptr
}

func (e *stackError) Error() string         { return e.msg }
func (e *stackError) StackTrace() []uintptr { return e.pcs }

// captureCallers returns program counters for the current call stack.
func captureCallers() []uintptr {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(1, pcs)
	return pcs[:n]
}

// --- CaptureException tests ---

func TestCaptureException_Nil(t *testing.T) {
	result := CaptureException(nil)
	if result != nil {
		t.Errorf("expected nil for nil error, got %v", result)
	}
}

func TestCaptureException_SimpleError(t *testing.T) {
	err := errors.New("something went wrong")
	result := CaptureException(err)

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	msg, _ := result["message"].(string)
	if msg != "something went wrong" {
		t.Errorf("message = %q, want %q", msg, "something went wrong")
	}

	typ, _ := result["type"].(string)
	if typ != "*errors.errorString" {
		t.Errorf("type = %q, want %q", typ, "*errors.errorString")
	}

	plat, _ := result["platform"].(string)
	if plat != "go" {
		t.Errorf("platform = %q, want %q", plat, "go")
	}

	stack, ok := result["stack"].(string)
	if !ok || stack == "" {
		t.Error("expected non-empty stack trace")
	}

	frames, ok := result["frames"].([]map[string]any)
	if !ok || len(frames) == 0 {
		t.Error("expected non-empty frames")
	}
}

func TestCaptureException_TypedError(t *testing.T) {
	err := &customError{Code: 404, Msg: "not found"}
	result := CaptureException(err)

	typ, _ := result["type"].(string)
	if typ != "*exceptions.customError" {
		t.Errorf("type = %q, want %q", typ, "*exceptions.customError")
	}

	msg, _ := result["message"].(string)
	if msg != "code 404: not found" {
		t.Errorf("message = %q, want %q", msg, "code 404: not found")
	}
}

func TestCaptureException_FmtErrorf(t *testing.T) {
	inner := errors.New("root cause")
	err := fmt.Errorf("wrapper: %w", inner)
	result := CaptureException(err)

	msg, _ := result["message"].(string)
	if msg != "wrapper: root cause" {
		t.Errorf("message = %q, want %q", msg, "wrapper: root cause")
	}

	chain, ok := result["chained_errors"].([]map[string]any)
	if !ok || len(chain) == 0 {
		t.Fatal("expected chained_errors for wrapped error")
	}

	chainMsg, _ := chain[0]["message"].(string)
	if chainMsg != "root cause" {
		t.Errorf("chained message = %q, want %q", chainMsg, "root cause")
	}
}

func TestCaptureException_WrappedChain(t *testing.T) {
	root := errors.New("root")
	mid := &wrappedError{msg: "mid: root", inner: root}
	outer := &wrappedError{msg: "outer: mid: root", inner: mid}

	result := CaptureException(outer)

	chain, ok := result["chained_errors"].([]map[string]any)
	if !ok {
		t.Fatal("expected chained_errors")
	}
	if len(chain) != 2 {
		t.Fatalf("expected 2 chained errors, got %d", len(chain))
	}

	if chain[0]["message"] != "mid: root" {
		t.Errorf("chain[0].message = %q, want %q", chain[0]["message"], "mid: root")
	}
	if chain[1]["message"] != "root" {
		t.Errorf("chain[1].message = %q, want %q", chain[1]["message"], "root")
	}
}

func TestCaptureException_NoChainForSimple(t *testing.T) {
	err := errors.New("plain error")
	result := CaptureException(err)

	if _, ok := result["chained_errors"]; ok {
		t.Error("expected no chained_errors for simple error")
	}
}

func TestCaptureException_StackTracerInterface(t *testing.T) {
	pcs := captureCallers()
	err := &stackError{msg: "stack error", pcs: pcs}
	result := CaptureException(err)

	// MCPCat SDK frames are skipped by shouldSkipFrame, so the stackTracer
	// frames (captured from this test file) will mostly be filtered out.
	// The important thing is that CaptureException doesn't panic and
	// returns a valid result with a stack trace (from stackTracer or runtime.Stack fallback).
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if _, ok := result["stack"].(string); !ok {
		t.Error("expected stack trace (from stackTracer or runtime.Stack fallback)")
	}
}

func TestCaptureException_MessageTruncation(t *testing.T) {
	longMsg := strings.Repeat("x", 5000)
	err := errors.New(longMsg)
	result := CaptureException(err)

	msg, _ := result["message"].(string)
	if len(msg) > maxMessageLength {
		t.Errorf("message length %d exceeds max %d", len(msg), maxMessageLength)
	}
	if !strings.HasSuffix(msg, truncationSuffix) {
		t.Error("expected truncated message to end with suffix")
	}
}

// --- parseGoStackTrace tests ---

func TestParseGoStackTrace(t *testing.T) {
	sampleStack := `goroutine 1 [running]:
runtime/debug.Stack()
	/usr/local/go/src/runtime/debug/stack.go:24 +0x5e
github.com/myapp/server.handleRequest(0xc0000b6000)
	/home/user/myapp/server/handler.go:42 +0x1a3
main.main()
	/home/user/myapp/main.go:15 +0x25
`
	frames := parseGoStackTrace(sampleStack)

	// runtime/debug.Stack is skipped entirely by shouldSkipFrame
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames (runtime skipped), got %d", len(frames))
	}

	// First remaining frame: user code
	f0 := frames[0]
	if f0["function"] != "github.com/myapp/server.handleRequest" {
		t.Errorf("frame[0].function = %q", f0["function"])
	}
	if f0["in_app"] != true {
		t.Error("user code should be in_app")
	}

	// Second frame: main
	f1 := frames[1]
	if f1["function"] != "main.main" {
		t.Errorf("frame[1].function = %q", f1["function"])
	}
	if f1["in_app"] != true {
		t.Error("main.main should be in_app")
	}
}

func TestParseGoStackTrace_MethodReceiver(t *testing.T) {
	stack := `goroutine 1 [running]:
github.com/myapp/db.(*Client).Query(0xc0000b6000, {0x1234, 0x5})
	/home/user/myapp/db/client.go:100 +0x45
`
	frames := parseGoStackTrace(stack)

	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}

	fn := frames[0]["function"].(string)
	if fn != "github.com/myapp/db.(*Client).Query" {
		t.Errorf("function = %q, want github.com/myapp/db.(*Client).Query", fn)
	}
}

func TestParseGoStackTrace_Empty(t *testing.T) {
	frames := parseGoStackTrace("")
	if len(frames) != 0 {
		t.Errorf("expected 0 frames for empty stack, got %d", len(frames))
	}
}

// --- isInApp tests ---

func TestIsInApp(t *testing.T) {
	tests := []struct {
		funcName string
		filePath string
		want     bool
	}{
		// Standard library — in_app=false (GOROOT check)
		{"runtime/debug.Stack", runtime.GOROOT() + "/src/runtime/debug/stack.go", false},
		{"net/http.(*Server).Serve", runtime.GOROOT() + "/src/net/http/server.go", false},
		{"fmt.Sprintf", runtime.GOROOT() + "/src/fmt/print.go", false},
		// Vendored dependencies — in_app=false (path-segment match on /vendor/)
		{"github.com/mycompany/myapp/vendor/github.com/lib/pq.(*conn).Query", "/home/user/myapp/vendor/github.com/lib/pq/conn.go", false},
		// third_party directory — in_app=false (path-segment match on /third_party/)
		{"github.com/mycompany/myapp/third_party/proto.Marshal", "/home/user/myapp/third_party/proto/marshal.go", false},
		// Package names containing "vendor" or "third_party" as substrings should NOT be falsely excluded
		{"github.com/mycompany/inventory-vendor-api.ListItems", "/home/user/myapp/inventory-vendor-api/items.go", true},
		{"github.com/mycompany/third_party_utils.Format", "/home/user/myapp/third_party_utils/format.go", true},
		// MCP libraries — in_app=true (NOT excluded)
		{"github.com/mark3labs/mcp-go/server.(*MCPServer).handleToolCall", "/path/mcp-go/server/server.go", true},
		{"github.com/modelcontextprotocol/go-sdk/mcp.handleReceive", "/path/go-sdk/mcp/shared.go", true},
		// User code — in_app=true
		{"github.com/mycompany/myapp/handler.ProcessTool", "/home/user/myapp/handler/tool.go", true},
		{"github.com/mycompany/myapp/db.(*Client).Query", "/home/user/myapp/db/client.go", true},
		// main package — in_app=true
		{"main.main", "/home/user/myapp/main.go", true},
		// Other third-party libs (not vendored) — in_app=true
		{"github.com/jackc/pgx/v5.(*Conn).Query", "/home/user/go/pkg/mod/github.com/jackc/pgx/v5/conn.go", true},
		{"go.uber.org/zap.(*Logger).Info", "/home/user/go/pkg/mod/go.uber.org/zap/logger.go", true},
	}

	for _, tt := range tests {
		t.Run(tt.funcName, func(t *testing.T) {
			got := isInApp(tt.funcName, tt.filePath)
			if got != tt.want {
				t.Errorf("isInApp(%q, %q) = %v, want %v", tt.funcName, tt.filePath, got, tt.want)
			}
		})
	}
}

func TestShouldSkipFrame(t *testing.T) {
	tests := []struct {
		funcName string
		want     bool
	}{
		// Runtime frames should be skipped (including sub-packages like runtime/debug)
		{"runtime.goexit", true},
		{"runtime.main", true},
		{"runtime/debug.Stack", true},
		// Testing frames should be skipped
		{"testing.tRunner", true},
		{"testing.(*T).Run", true},
		// MCPCat SDK frames should be skipped
		{"go.agentcat.com/sdk/internal/exceptions.CaptureException", true},
		{"go.agentcat.com/sdk/internal/event.NewEvent", true},
		{"go.agentcat.com/sdk/internal/publisher.(*Publisher).Publish", true},
		{"go.agentcat.com/sdk/mcpgo.addTracingToHooks", true},
		{"go.agentcat.com/sdk/officialsdk.newTrackingMiddleware", true},
		// MCPCat SDK _test packages should NOT be skipped (needed for testing)
		{"go.agentcat.com/sdk/internal/exceptions_test.TestShouldSkipFrame", false},
		{"go.agentcat.com/sdk/mcpgo_test.TestErrorTracking", false},
		// User code should NOT be skipped
		{"github.com/mycompany/myapp/handler.ProcessTool", false},
		{"main.main", false},
		// MCP libraries should NOT be skipped
		{"github.com/mark3labs/mcp-go/server.(*MCPServer).handleToolCall", false},
		{"github.com/modelcontextprotocol/go-sdk/mcp.handleReceive", false},
		// Stdlib (non-runtime) should NOT be skipped (they get in_app=false instead)
		{"net/http.(*Server).Serve", false},
		{"fmt.Sprintf", false},
	}

	for _, tt := range tests {
		t.Run(tt.funcName, func(t *testing.T) {
			got := shouldSkipFrame(tt.funcName)
			if got != tt.want {
				t.Errorf("shouldSkipFrame(%q) = %v, want %v", tt.funcName, got, tt.want)
			}
		})
	}
}

// --- makeRelativePath tests ---

func TestMakeRelativePath(t *testing.T) {
	goroot := runtime.GOROOT()

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "empty",
			path: "",
			want: "",
		},
		{
			name: "GOROOT path",
			path: goroot + "/src/runtime/debug/stack.go",
			want: "runtime/debug/stack.go",
		},
		{
			name: "deployment path /app/",
			path: "/app/server/main.go",
			want: "server/main.go",
		},
		{
			name: "deployment path /var/task/",
			path: "/var/task/handler.go",
			want: "handler.go",
		},
		{
			name: "already relative",
			path: "handler.go",
			want: "handler.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeRelativePath(tt.path)
			if got != tt.want {
				t.Errorf("makeRelativePath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// --- truncation tests ---

func TestTruncateMessage(t *testing.T) {
	short := "short message"
	if got := truncateMessage(short); got != short {
		t.Errorf("short message should not be truncated, got %q", got)
	}

	long := strings.Repeat("a", 3000)
	got := truncateMessage(long)
	if len(got) > maxMessageLength {
		t.Errorf("truncated length %d exceeds max %d", len(got), maxMessageLength)
	}
	if !strings.HasSuffix(got, truncationSuffix) {
		t.Error("expected truncation suffix")
	}
}

func TestTruncateFrames(t *testing.T) {
	// Under limit
	small := make([]map[string]any, 10)
	for i := range small {
		small[i] = map[string]any{"lineno": i}
	}
	if got := truncateFrames(small); len(got) != 10 {
		t.Errorf("small frames should not be truncated, got %d", len(got))
	}

	// Over limit: should keep first 25 + last 25
	large := make([]map[string]any, 100)
	for i := range large {
		large[i] = map[string]any{"lineno": i}
	}
	got := truncateFrames(large)
	if len(got) != maxStackFrames {
		t.Errorf("expected %d frames, got %d", maxStackFrames, len(got))
	}

	// Verify first 25 are from the beginning
	for i := 0; i < 25; i++ {
		if got[i]["lineno"] != i {
			t.Errorf("first half: got[%d].lineno = %v, want %d", i, got[i]["lineno"], i)
			break
		}
	}
	// Verify last 25 are from the end
	for i := 0; i < 25; i++ {
		expected := 75 + i
		if got[25+i]["lineno"] != expected {
			t.Errorf("second half: got[%d].lineno = %v, want %d", 25+i, got[25+i]["lineno"], expected)
			break
		}
	}
}

// --- unwrapErrorChain tests ---

func TestUnwrapErrorChain_MaxDepth(t *testing.T) {
	// Build a chain deeper than maxChainDepth
	var err error = errors.New("bottom")
	for i := 0; i < maxChainDepth+5; i++ {
		err = fmt.Errorf("wrap %d: %w", i, err)
	}

	result := CaptureException(err)
	chain, ok := result["chained_errors"].([]map[string]any)
	if !ok {
		t.Fatal("expected chained_errors")
	}
	if len(chain) > maxChainDepth {
		t.Errorf("chain length %d exceeds max depth %d", len(chain), maxChainDepth)
	}
}

// --- parseFuncName tests ---

func TestParseFuncName(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"main.main()", "main.main"},
		{"github.com/foo/bar.Baz(0x1, 0x2)", "github.com/foo/bar.Baz"},
		{"github.com/foo/bar.(*T).Method(0xc000)", "github.com/foo/bar.(*T).Method"},
		{"runtime.goexit()", "runtime.goexit"},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got := parseFuncName(tt.line)
			if got != tt.want {
				t.Errorf("parseFuncName(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

// --- parseFileLine tests ---

func TestParseFileLine(t *testing.T) {
	tests := []struct {
		line     string
		wantPath string
		wantLine int
	}{
		{"/path/to/file.go:42 +0x1a3", "/path/to/file.go", 42},
		{"/path/to/file.go:100", "/path/to/file.go", 100},
		{"/usr/local/go/src/runtime/proc.go:271 +0x26", "/usr/local/go/src/runtime/proc.go", 271},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			gotPath, gotLine := parseFileLine(tt.line)
			if gotPath != tt.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if gotLine != tt.wantLine {
				t.Errorf("line = %d, want %d", gotLine, tt.wantLine)
			}
		})
	}
}

// --- Edge case: stackTracer with empty PCs ---

func TestFramesFromStackTracer_EmptyPCs(t *testing.T) {
	err := &stackError{msg: "no pcs", pcs: nil}
	result := CaptureException(err)

	// Should fall back to runtime.Stack() since StackTrace() returns nil
	if _, ok := result["stack"].(string); !ok {
		t.Error("expected stack from runtime.Stack() fallback")
	}
}

func TestFramesFromStackTracer_ZeroPCs(t *testing.T) {
	err := &stackError{msg: "zero pcs", pcs: []uintptr{}}
	result := CaptureException(err)

	// Empty PCs → framesFromStackTracer returns nil → falls back to runtime.Stack()
	if _, ok := result["stack"].(string); !ok {
		t.Error("expected stack from runtime.Stack() fallback")
	}
}

// --- Edge case: funcOrDefault with empty string ---

func TestFuncOrDefault(t *testing.T) {
	if got := funcOrDefault(""); got != "<unknown>" {
		t.Errorf("funcOrDefault(\"\") = %q, want %q", got, "<unknown>")
	}
	if got := funcOrDefault("main.foo"); got != "main.foo" {
		t.Errorf("funcOrDefault(\"main.foo\") = %q, want %q", got, "main.foo")
	}
}

// --- Edge case: parseFuncName without parentheses ---

func TestParseFuncName_NoParens(t *testing.T) {
	got := parseFuncName("somebare.function")
	if got != "somebare.function" {
		t.Errorf("parseFuncName without parens = %q, want %q", got, "somebare.function")
	}
}

// --- Edge case: parseFileLine without colon ---

func TestParseFileLine_NoColon(t *testing.T) {
	path, lineno := parseFileLine("no-colon-here")
	if path != "no-colon-here" {
		t.Errorf("path = %q, want %q", path, "no-colon-here")
	}
	if lineno != 0 {
		t.Errorf("lineno = %d, want 0", lineno)
	}
}

// --- Edge case: parseFileLine with non-numeric line number ---

func TestParseFileLine_NonNumericLine(t *testing.T) {
	path, lineno := parseFileLine("/path/to/file.go:abc")
	if path != "/path/to/file.go" {
		t.Errorf("path = %q, want %q", path, "/path/to/file.go")
	}
	if lineno != 0 {
		t.Errorf("lineno = %d, want 0 for non-numeric", lineno)
	}
}

// --- Edge case: parseGoStackTrace with malformed lines ---

func TestParseGoStackTrace_MalformedLines(t *testing.T) {
	// Lines that don't look like file references should be skipped
	stack := `goroutine 1 [running]:
some-random-text-without-colon
another-line-without-colon
github.com/myapp/handler.Do(0xc000)
	/home/user/myapp/handler.go:10 +0x25
`
	frames := parseGoStackTrace(stack)
	if len(frames) != 1 {
		t.Errorf("expected 1 frame (skipping malformed), got %d", len(frames))
	}
	if len(frames) > 0 && frames[0]["function"] != "github.com/myapp/handler.Do" {
		t.Errorf("frame function = %q", frames[0]["function"])
	}
}

func TestParseGoStackTrace_EmptyLinesInStack(t *testing.T) {
	stack := `goroutine 1 [running]:

github.com/myapp/handler.Do(0xc000)
	/home/user/myapp/handler.go:10 +0x25

`
	frames := parseGoStackTrace(stack)
	if len(frames) != 1 {
		t.Errorf("expected 1 frame, got %d", len(frames))
	}
}

// --- Edge case: unwrapErrorChain with stackTracer in chain ---

// chainedStackError wraps another error and also carries a stack trace.
type chainedStackError struct {
	msg   string
	inner error
	pcs   []uintptr
}

func (e *chainedStackError) Error() string         { return e.msg }
func (e *chainedStackError) Unwrap() error         { return e.inner }
func (e *chainedStackError) StackTrace() []uintptr { return e.pcs }

func TestUnwrapErrorChain_ChainedWithStackTracer(t *testing.T) {
	pcs := captureCallers()
	inner := &chainedStackError{msg: "inner with stack", inner: nil, pcs: pcs}
	outer := &wrappedError{msg: "outer", inner: inner}

	result := CaptureException(outer)

	chain, ok := result["chained_errors"].([]map[string]any)
	if !ok || len(chain) == 0 {
		t.Fatal("expected chained_errors")
	}

	// The chained error should have stack from its StackTrace() interface.
	// MCPCat SDK frames are skipped by shouldSkipFrame, so structured frames
	// may be empty, but the raw stack text should still be present.
	if _, ok := chain[0]["stack"].(string); !ok {
		t.Error("expected raw stack on chained error from StackTrace() interface")
	}
}

// --- Edge case: unwrapErrorChain circular reference protection ---

// circularError creates a circular Unwrap chain.
type circularError struct {
	msg  string
	next error
}

func (e *circularError) Error() string { return e.msg }
func (e *circularError) Unwrap() error { return e.next }

func TestUnwrapErrorChain_CircularReference(t *testing.T) {
	a := &circularError{msg: "error A"}
	b := &circularError{msg: "error B", next: a}
	a.next = b // Create circular reference

	result := CaptureException(a)

	chain, ok := result["chained_errors"].([]map[string]any)
	if !ok {
		t.Fatal("expected chained_errors")
	}
	// Should stop after detecting the cycle, not loop forever.
	// Chain walks: Unwrap(a)=b (new, add), Unwrap(b)=a (new, add), Unwrap(a)=b (seen, stop)
	if len(chain) > maxChainDepth {
		t.Errorf("chain length %d should be bounded", len(chain))
	}
	if len(chain) != 2 {
		t.Errorf("expected 2 chained errors before cycle detection, got %d", len(chain))
	}
}

// --- Edge case: makeRelativePath with <unknown> sentinel ---

func TestMakeRelativePath_UnknownSentinel(t *testing.T) {
	got := makeRelativePath("<unknown>")
	if got != "<unknown>" {
		t.Errorf("makeRelativePath(<unknown>) = %q, want %q", got, "<unknown>")
	}
}

// --- Edge case: makeRelativePath with home directory ---

func TestMakeRelativePath_HomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	path := home + "/projects/myapp/main.go"
	got := makeRelativePath(path)
	if strings.HasPrefix(got, home) {
		t.Errorf("expected home directory to be stripped, got %q", got)
	}
	if !strings.HasPrefix(got, "~") {
		t.Errorf("expected tilde prefix, got %q", got)
	}
}

// --- Edge case: makeRelativePath with GOPATH ---

func TestMakeRelativePath_GOPATH(t *testing.T) {
	origGopath := os.Getenv("GOPATH")
	t.Setenv("GOPATH", "/tmp/fakego")
	defer func() {
		if origGopath != "" {
			os.Setenv("GOPATH", origGopath)
		}
	}()

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "GOPATH src",
			path: "/tmp/fakego/src/github.com/user/pkg/file.go",
			want: "github.com/user/pkg/file.go",
		},
		{
			name: "GOPATH pkg/mod",
			path: "/tmp/fakego/pkg/mod/github.com/user/pkg@v1.0.0/file.go",
			want: "github.com/user/pkg@v1.0.0/file.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeRelativePath(tt.path)
			if got != tt.want {
				t.Errorf("makeRelativePath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// --- Edge case: extractStackTracer finds it in inner error ---

func TestExtractStackTracer_InWrappedError(t *testing.T) {
	pcs := captureCallers()
	inner := &stackError{msg: "inner", pcs: pcs}
	outer := fmt.Errorf("wrapper: %w", inner)

	result := CaptureException(outer)

	// CaptureException should not panic and should return a valid result.
	// The inner error's StackTrace() PCs are from the MCPCat SDK test package,
	// so shouldSkipFrame filters them out. The function falls back to
	// runtime.Stack() when the stackTracer frames are empty.
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if _, ok := result["stack"].(string); !ok {
		t.Error("expected stack trace (from stackTracer or runtime.Stack fallback)")
	}
}

// --- Integration: CaptureException returns all expected top-level keys ---

func TestCaptureException_AllFields(t *testing.T) {
	err := errors.New("test error")
	result := CaptureException(err)

	requiredKeys := []string{"message", "type", "platform", "stack", "frames"}
	for _, key := range requiredKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("missing required key %q", key)
		}
	}
}
