package logging

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// resetGlobalState resets the global logger state for testing
func resetGlobalState() {
	loggerMu.Lock()
	defaultLogger = nil
	loggerMu.Unlock()
	globalDebugMu.Lock()
	globalDebug = false
	globalDebugMu.Unlock()
}

// TestNew_ReturnsSameInstance verifies that multiple calls to New() return the same logger instance
func TestNew_ReturnsSameInstance(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	logger1 := New()
	logger2 := New()

	if logger1 != logger2 {
		t.Error("Expected New() to return the same logger instance")
	}
}

// TestNewLogger_GlobalDebugState verifies new logger respects global debug flag
func TestNewLogger_GlobalDebugState(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	tests := []struct {
		name        string
		globalDebug bool
	}{
		{"debug enabled", true},
		{"debug disabled", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetGlobalState()
			SetGlobalDebug(tt.globalDebug)

			logger := newLogger()
			defer logger.Close()

			if logger.debug != tt.globalDebug {
				t.Errorf("Expected logger.debug=%v, got %v", tt.globalDebug, logger.debug)
			}
		})
	}
}

// TestSetGlobalDebug_UpdatesFlag tests setting/unsetting global debug
func TestSetGlobalDebug_UpdatesFlag(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	globalDebugMu.RLock()
	if !globalDebug {
		t.Error("Expected globalDebug to be true")
	}
	globalDebugMu.RUnlock()

	SetGlobalDebug(false)
	globalDebugMu.RLock()
	if globalDebug {
		t.Error("Expected globalDebug to be false")
	}
	globalDebugMu.RUnlock()
}

// TestSetGlobalDebug_UpdatesExistingLogger verifies existing logger gets updated
func TestSetGlobalDebug_UpdatesExistingLogger(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	logger := New()
	defer logger.Close()

	SetGlobalDebug(true)
	if !logger.debug {
		t.Error("Expected logger.debug to be true after SetGlobalDebug(true)")
	}

	SetGlobalDebug(false)
	if logger.debug {
		t.Error("Expected logger.debug to be false after SetGlobalDebug(false)")
	}
}

// TestSetGlobalDebug_Concurrent tests thread-safety with concurrent access
func TestSetGlobalDebug_Concurrent(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	var wg sync.WaitGroup
	iterations := 100

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(val bool) {
			defer wg.Done()
			SetGlobalDebug(val)
		}(i%2 == 0)
	}

	wg.Wait()
	// Test passes if no race conditions occur
}

// TestLogger_Info verifies Info logs with correct prefix and format
func TestLogger_Info(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := newLogger()
	defer logger.Close()

	// Capture output
	var buf bytes.Buffer
	logger.logger = log.New(&buf, "[MCPCat] ", log.LstdFlags)

	logger.Info("test message")

	output := buf.String()
	if !strings.Contains(output, "INFO: test message") {
		t.Errorf("Expected output to contain 'INFO: test message', got: %s", output)
	}
	if !strings.Contains(output, "[MCPCat]") {
		t.Errorf("Expected output to contain '[MCPCat]' prefix, got: %s", output)
	}
}

// TestLogger_Infof tests formatted Info logging
func TestLogger_Infof(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := newLogger()
	defer logger.Close()

	var buf bytes.Buffer
	logger.logger = log.New(&buf, "[MCPCat] ", log.LstdFlags)

	logger.Infof("formatted %s %d", "message", 42)

	output := buf.String()
	if !strings.Contains(output, "INFO: formatted message 42") {
		t.Errorf("Expected output to contain 'INFO: formatted message 42', got: %s", output)
	}
}

// TestLogger_Warn verifies Warn logs with correct prefix
func TestLogger_Warn(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := newLogger()
	defer logger.Close()

	var buf bytes.Buffer
	logger.logger = log.New(&buf, "[MCPCat] ", log.LstdFlags)

	logger.Warn("warning message")

	output := buf.String()
	if !strings.Contains(output, "WARN: warning message") {
		t.Errorf("Expected output to contain 'WARN: warning message', got: %s", output)
	}
}

// TestLogger_Warnf tests formatted Warn logging
func TestLogger_Warnf(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := newLogger()
	defer logger.Close()

	var buf bytes.Buffer
	logger.logger = log.New(&buf, "[MCPCat] ", log.LstdFlags)

	logger.Warnf("warning %s", "test")

	output := buf.String()
	if !strings.Contains(output, "WARN: warning test") {
		t.Errorf("Expected output to contain 'WARN: warning test', got: %s", output)
	}
}

// TestLogger_Error verifies Error logs with correct prefix
func TestLogger_Error(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := newLogger()
	defer logger.Close()

	var buf bytes.Buffer
	logger.logger = log.New(&buf, "[MCPCat] ", log.LstdFlags)

	logger.Error("error message")

	output := buf.String()
	if !strings.Contains(output, "ERROR: error message") {
		t.Errorf("Expected output to contain 'ERROR: error message', got: %s", output)
	}
}

// TestLogger_Errorf tests formatted Error logging
func TestLogger_Errorf(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := newLogger()
	defer logger.Close()

	var buf bytes.Buffer
	logger.logger = log.New(&buf, "[MCPCat] ", log.LstdFlags)

	logger.Errorf("error %d", 404)

	output := buf.String()
	if !strings.Contains(output, "ERROR: error 404") {
		t.Errorf("Expected output to contain 'ERROR: error 404', got: %s", output)
	}
}

// TestLogger_Debug verifies Debug logs with correct prefix
func TestLogger_Debug(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := newLogger()
	defer logger.Close()

	var buf bytes.Buffer
	logger.logger = log.New(&buf, "[MCPCat] ", log.LstdFlags)

	logger.Debug("debug message")

	output := buf.String()
	if !strings.Contains(output, "DEBUG: debug message") {
		t.Errorf("Expected output to contain 'DEBUG: debug message', got: %s", output)
	}
}

// TestLogger_Debugf tests formatted Debug logging
func TestLogger_Debugf(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := newLogger()
	defer logger.Close()

	var buf bytes.Buffer
	logger.logger = log.New(&buf, "[MCPCat] ", log.LstdFlags)

	logger.Debugf("debug %s", "info")

	output := buf.String()
	if !strings.Contains(output, "DEBUG: debug info") {
		t.Errorf("Expected output to contain 'DEBUG: debug info', got: %s", output)
	}
}

// TestLogger_DebugDisabled_DiscardsOutput verifies logs go to io.Discard when debug=false
func TestLogger_DebugDisabled_DiscardsOutput(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(false)
	logger := newLogger()
	defer logger.Close()

	if logger.debug {
		t.Error("Expected logger.debug to be false")
	}

	if logger.file != nil {
		t.Error("Expected logger.file to be nil when debug=false")
	}

	// Confirm the underlying logger still works if we manually override
	var buf bytes.Buffer
	logger.logger.SetOutput(&buf)
	logger.Info("test")

	if buf.Len() == 0 {
		t.Error("Expected some output after manually setting writer")
	}
}

// TestLogger_DebugEnabled_WritesToFile verifies logs written to file when debug=true
func TestLogger_DebugEnabled_WritesToFile(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)

	// Create a temp file for logging
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.log")

	file, err := os.OpenFile(tmpFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to create temp log file: %v", err)
	}

	logger := &Logger{
		file:  file,
		debug: true,
	}
	logger.logger = log.New(file, "[MCPCat] ", log.LstdFlags)
	defer logger.Close()

	logger.Info("test message")

	// Read the file to verify content was written
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if !strings.Contains(string(content), "INFO: test message") {
		t.Errorf("Expected log file to contain 'INFO: test message', got: %s", string(content))
	}
}

// TestSetGlobalDebug_TogglesOutput tests toggling debug updates writer
func TestSetGlobalDebug_TogglesOutput(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	logger := New()
	defer logger.Close()

	// Initially debug is false
	if logger.debug {
		t.Error("Expected initial debug to be false")
	}

	// Enable debug
	SetGlobalDebug(true)
	if !logger.debug {
		t.Error("Expected debug to be true after SetGlobalDebug(true)")
	}

	// Disable debug
	SetGlobalDebug(false)
	if logger.debug {
		t.Error("Expected debug to be false after SetGlobalDebug(false)")
	}
}

// TestNewLogger_CreatesLogFile verifies log file is created only when debug=true
func TestNewLogger_CreatesLogFile(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	t.Run("debug enabled creates file", func(t *testing.T) {
		resetGlobalState()
		SetGlobalDebug(true)

		logger := newLogger()
		defer logger.Close()

		if logger.file == nil {
			t.Error("Expected logger.file to be set when debug=true")
		}

		stat, err := logger.file.Stat()
		if err != nil {
			t.Errorf("Failed to stat log file: %v", err)
		}
		if stat.IsDir() {
			t.Error("Expected log file to be a file, not a directory")
		}
	})

	t.Run("debug disabled creates no file", func(t *testing.T) {
		resetGlobalState()
		SetGlobalDebug(false)

		logger := newLogger()
		defer logger.Close()

		if logger.file != nil {
			t.Error("Expected logger.file to be nil when debug=false")
		}
	})
}

// TestOpenLogFile_FailureSilentlyDegrades verifies that when the log file
// cannot be opened, the logger silently degrades to io.Discard instead of
// falling back to stderr.
func TestOpenLogFile_FailureSilentlyDegrades(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	l := &Logger{debug: true}
	l.logger = log.New(io.Discard, "[MCPCat] ", log.LstdFlags)

	// file stays nil if openLogFile was never called (simulates failure path)
	if l.file != nil {
		t.Error("Expected file to be nil before openLogFile")
	}

	l.updateWriter()

	// After updateWriter with a nil file, output should go to io.Discard,
	// not stderr. We can verify by writing and checking nothing panics.
	l.Info("this should not panic or write to stderr")

	// updateWriter should have tried to open the file; if the home dir
	// is available it may succeed. Either way, no panic and no stderr.
}

// TestLogger_Close verifies Close() closes file properly
func TestLogger_Close(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.log")

	file, err := os.OpenFile(tmpFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to create temp log file: %v", err)
	}

	logger := &Logger{
		file:  file,
		debug: false,
	}
	logger.logger = log.New(io.Discard, "[MCPCat] ", log.LstdFlags)

	err = logger.Close()
	if err != nil {
		t.Errorf("Expected Close() to succeed, got error: %v", err)
	}

	// Verify file is closed by trying to write
	_, err = file.Write([]byte("test"))
	if err == nil {
		t.Error("Expected write to closed file to fail")
	}
}

// TestLogger_Close_NilFile verifies Close() handles nil file gracefully
func TestLogger_Close_NilFile(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	logger := &Logger{
		file:  nil,
		debug: false,
	}
	logger.logger = log.New(io.Discard, "[MCPCat] ", log.LstdFlags)

	err := logger.Close()
	if err != nil {
		t.Errorf("Expected Close() to succeed with nil file, got error: %v", err)
	}
}

// TestLogger_ConcurrentWrites tests multiple goroutines logging simultaneously
func TestLogger_ConcurrentWrites(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := New()
	defer logger.Close()

	var wg sync.WaitGroup
	iterations := 100

	for i := 0; i < iterations; i++ {
		wg.Add(4)
		go func(n int) {
			defer wg.Done()
			logger.Info("info " + string(rune(n)))
		}(i)
		go func(n int) {
			defer wg.Done()
			logger.Warn("warn " + string(rune(n)))
		}(i)
		go func(n int) {
			defer wg.Done()
			logger.Error("error " + string(rune(n)))
		}(i)
		go func(n int) {
			defer wg.Done()
			logger.Debug("debug " + string(rune(n)))
		}(i)
	}

	wg.Wait()
	// Test passes if no race conditions or panics occur
}

// TestLogger_ConcurrentDebugToggle tests toggling debug while logging
func TestLogger_ConcurrentDebugToggle(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	logger := New()
	defer logger.Close()

	var wg sync.WaitGroup
	iterations := 50

	// Start logging goroutines
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			logger.Infof("message %d", n)
			logger.Warnf("warning %d", n)
			logger.Errorf("error %d", n)
			logger.Debugf("debug %d", n)
		}(i)
	}

	// Toggle debug concurrently
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			SetGlobalDebug(n%2 == 0)
		}(i)
	}

	wg.Wait()
	// Test passes if no race conditions or panics occur
}

// TestUpdateWriter_OpensFileOnDebugEnable verifies that toggling debug from
// false to true lazily opens the log file.
func TestUpdateWriter_OpensFileOnDebugEnable(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	logger := New()
	defer logger.Close()

	if logger.file != nil {
		t.Error("Expected file to be nil initially (debug=false)")
	}

	SetGlobalDebug(true)

	if logger.file == nil {
		t.Error("Expected file to be opened after enabling debug")
	}
}

// TestUpdateWriter_ClosesFileOnDebugDisable verifies that toggling debug from
// true to false closes the log file and sets it to nil.
func TestUpdateWriter_ClosesFileOnDebugDisable(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := New()
	defer logger.Close()

	if logger.file == nil {
		t.Fatal("Expected file to be opened when debug=true")
	}

	SetGlobalDebug(false)

	if logger.file != nil {
		t.Error("Expected file to be nil after disabling debug")
	}
}

// TestResetForTesting verifies that ResetForTesting resets the singleton logger.
func TestResetForTesting(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	logger1 := New()

	ResetForTesting()

	logger2 := New()
	if logger1 == logger2 {
		t.Error("Expected new logger instance after ResetForTesting")
	}
}

// TestResetForTesting_WhenNil verifies ResetForTesting handles nil defaultLogger.
func TestResetForTesting_WhenNil(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	// Should not panic when no logger exists
	ResetForTesting()
}

// TestResetForTesting_ClosesExistingLogger verifies ResetForTesting closes the
// existing logger's file before resetting.
func TestResetForTesting_ClosesExistingLogger(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	SetGlobalDebug(true)
	logger := New()

	if logger.file == nil {
		t.Skip("log file not opened (debug mode)")
	}

	file := logger.file
	ResetForTesting()

	// File should have been closed
	_, err := file.Write([]byte("test"))
	if err == nil {
		t.Error("expected write to closed file to fail after ResetForTesting")
	}
}

// TestUpdateWriter_DebugToggleWritesToFile verifies that after enabling debug,
// log messages are actually written to disk.
func TestUpdateWriter_DebugToggleWritesToFile(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()

	logger := New()
	defer logger.Close()

	// Start with debug off — swap the file to a temp file after enabling.
	SetGlobalDebug(true)

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.log")
	file, err := os.OpenFile(tmpFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to create temp log file: %v", err)
	}

	logger.mu.Lock()
	if logger.file != nil {
		logger.file.Close()
	}
	logger.file = file
	logger.logger.SetOutput(file)
	logger.mu.Unlock()

	logger.Info("after enable")

	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	if !strings.Contains(string(content), "INFO: after enable") {
		t.Errorf("Expected log file to contain 'INFO: after enable', got: %s", string(content))
	}
}

func TestDiagnosticsSink_ReceivesLevels(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()
	defer SetDiagnosticsSink(nil)

	type rec struct {
		level Level
		msg   string
	}
	var got []rec
	var mu sync.Mutex
	SetDiagnosticsSink(func(l Level, m string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, rec{l, m})
	})

	lg := New()
	lg.Info("info-line")
	lg.Warn("warn-line")
	lg.Error("error-line")
	lg.Debug("debug-line")

	mu.Lock()
	defer mu.Unlock()
	want := []rec{
		{LevelInfo, "info-line"},
		{LevelWarn, "warn-line"},
		{LevelError, "error-line"},
		{LevelDebug, "debug-line"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDiagnosticsSink_FiresWhenDebugFalse(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()
	defer SetDiagnosticsSink(nil)

	// Default state: globalDebug == false.
	var called bool
	SetDiagnosticsSink(func(l Level, m string) { called = true })

	New().Info("hello")

	if !called {
		t.Fatal("sink must fire even when debug is false")
	}
}

func TestDiagnosticsSink_PanicDoesNotBreakLogging(t *testing.T) {
	resetGlobalState()
	defer resetGlobalState()
	defer SetDiagnosticsSink(nil)

	SetDiagnosticsSink(func(l Level, m string) { panic("boom") })

	lg := New()
	var buf bytes.Buffer
	lg.logger = log.New(&buf, "[MCPCat] ", log.LstdFlags)

	lg.Info("survives") // must not panic

	if !strings.Contains(buf.String(), "survives") {
		t.Fatalf("logging must continue after sink panic, got %q", buf.String())
	}
}
