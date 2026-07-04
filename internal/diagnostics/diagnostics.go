package diagnostics

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"go.agentcat.com/sdk/internal/logging"
	"go.agentcat.com/sdk/internal/session"
)

var (
	mu           sync.Mutex
	initialized  bool
	enabled      bool
	buffer       []otlpLogRecord
	staticAttrs  []otlpAttribute
	flushPending bool
	sdkVersion   string

	httpClient = &http.Client{Timeout: 5 * time.Second}
)

// Init registers the diagnostics sink and builds static attributes. Idempotent:
// only the first call per process takes effect. Never panics.
//
// Diagnostics are enabled unless the DisableDiagnostics option is set, the
// DISABLE_DIAGNOSTICS env var disables them, or the process is a test binary.
// The go-test auto-disable protects every consumer's suite (not just ours) from
// shipping diagnostics; an explicit falsy DISABLE_DIAGNOSTICS (false/0/no/off)
// is a deliberate opt-in that overrides it.
func Init(projectID string, disabled bool, integration, mcpSDKPath string) {
	defer func() { _ = recover() }()

	mu.Lock()
	if initialized {
		mu.Unlock()
		return
	}
	initialized = true
	flag := envDiagnosticsFlag()
	en := !disabled && flag != envFlagDisabled &&
		(flag == envFlagForceEnabled || !isTestEnvironment())
	enabled = en
	mu.Unlock()

	if !en {
		return
	}

	attrs := buildStaticAttributes(projectID, integration, mcpSDKPath)
	ver := session.GetDependencyVersion(sdkModulePath)

	mu.Lock()
	staticAttrs = attrs
	sdkVersion = ver
	mu.Unlock()

	logging.SetDiagnosticsSink(capture)
}

// capture appends a record (drop-oldest at maxBuffer) and schedules a flush.
// Debug entries are ignored. Never panics, never blocks.
func capture(level logging.Level, msg string) {
	defer func() { _ = recover() }()
	if level == logging.LevelDebug {
		return
	}
	rec := buildRecord(level, msg)
	mu.Lock()
	if !enabled {
		mu.Unlock()
		return
	}
	if len(buffer) >= maxBuffer {
		buffer = buffer[1:]
	}
	buffer = append(buffer, rec)
	schedule := !flushPending
	if schedule {
		flushPending = true
	}
	mu.Unlock()

	if schedule {
		time.AfterFunc(batchFlush, Flush)
	}
}

// Flush sends the buffered batch best-effort. Never returns an error; never panics.
func Flush() {
	defer func() { _ = recover() }()

	mu.Lock()
	flushPending = false
	if !enabled || len(buffer) == 0 {
		mu.Unlock()
		return
	}
	records := buffer
	buffer = nil
	attrs := staticAttrs
	ver := sdkVersion
	mu.Unlock()

	payload := otlpPayload{
		ResourceLogs: []otlpResourceLogs{{
			Resource: otlpResource{Attributes: attrs},
			ScopeLogs: []otlpScopeLogs{{
				Scope:      otlpScope{Name: DiagnosticsScopeName, Version: ver},
				LogRecords: records,
			}},
		}},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, resolveEndpoint(), bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if token := resolveToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// Enabled reports whether diagnostics is active. For tests.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

// ResetForTest clears all package state and unregisters the sink. For tests.
func ResetForTest() {
	mu.Lock()
	initialized = false
	enabled = false
	buffer = nil
	staticAttrs = nil
	flushPending = false
	sdkVersion = ""
	mu.Unlock()
	logging.SetDiagnosticsSink(nil)
}

// StaticAttributesForTest returns the built resource attributes. For tests.
func StaticAttributesForTest() []otlpAttribute {
	mu.Lock()
	defer mu.Unlock()
	return staticAttrs
}

func bufferLenForTest() int {
	mu.Lock()
	defer mu.Unlock()
	return len(buffer)
}
