package exceptions

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

const (
	maxMessageLength = 2048
	maxStackFrames   = 50
	maxChainDepth    = 10
	platform         = "go"
	truncationSuffix = "..."
	unknownFunction  = "<unknown>"
	unknownFile      = "<unknown>"
)

var goRoot = strings.ReplaceAll(runtime.GOROOT(), "\\", "/")

// stackTracer is the interface implemented by errors that carry a stack trace,
// such as those created by github.com/pkg/errors.
type stackTracer interface {
	StackTrace() []uintptr
}

// CaptureException converts a Go error into a structured error map matching the
// MCPCat ErrorData schema (message, type, stack, frames, chained_errors, platform).
func CaptureException(err error) map[string]any {
	if err == nil {
		return nil
	}

	result := map[string]any{
		"message":  truncateMessage(err.Error()),
		"type":     fmt.Sprintf("%T", err),
		"platform": platform,
	}

	// Prefer stack trace from the error itself (pkg/errors and compatible libraries)
	// since it captures the origin. Fall back to runtime.Stack() which shows the
	// current goroutine's stack at this detection point.
	var rawStack string
	var frames []map[string]any

	if st, ok := extractStackTracer(err); ok {
		frames = framesFromStackTracer(st)
		rawStack = formatStackTracerRaw(st)
	}

	// Fall back to runtime.Stack() if the stackTracer was absent or returned empty PCs.
	if rawStack == "" && len(frames) == 0 {
		buf := make([]byte, 16384)
		n := runtime.Stack(buf, false)
		rawStack = string(buf[:n])
		frames = parseGoStackTrace(rawStack)
	}

	if rawStack != "" {
		result["stack"] = rawStack
	}
	if len(frames) > 0 {
		result["frames"] = truncateFrames(frames)
	}

	chainedErrors := unwrapErrorChain(err)
	if len(chainedErrors) > 0 {
		result["chained_errors"] = chainedErrors
	}

	return result
}

// extractStackTracer walks the error chain looking for one that implements stackTracer.
// Includes cycle detection to handle circular Unwrap chains safely.
func extractStackTracer(err error) (stackTracer, bool) {
	seen := make(map[error]bool)
	current := err
	for current != nil {
		if seen[current] {
			break
		}
		seen[current] = true
		if st, ok := current.(stackTracer); ok {
			return st, true
		}
		current = errors.Unwrap(current)
	}
	return nil, false
}

// framesFromStackTracer resolves program counters into structured frames.
func framesFromStackTracer(st stackTracer) []map[string]any {
	pcs := st.StackTrace()
	if len(pcs) == 0 {
		return nil
	}

	runtimeFrames := runtime.CallersFrames(pcs)
	var frames []map[string]any

	for {
		frame, more := runtimeFrames.Next()
		if frame.PC == 0 && !more {
			break
		}

		if frame.Function != "" || frame.File != "" {
			if shouldSkipFrame(frame.Function) {
				if !more {
					break
				}
				continue
			}
			f := map[string]any{
				"function": funcOrDefault(frame.Function),
				"filename": makeRelativePath(frame.File),
				"abs_path": frame.File,
				"lineno":   frame.Line,
				"in_app":   isInApp(frame.Function, frame.File),
			}
			frames = append(frames, f)
		}

		if !more {
			break
		}
	}

	return frames
}

// formatStackTracerRaw produces a raw text representation of a stackTracer for the "stack" field.
func formatStackTracerRaw(st stackTracer) string {
	pcs := st.StackTrace()
	if len(pcs) == 0 {
		return ""
	}

	runtimeFrames := runtime.CallersFrames(pcs)
	var b strings.Builder

	for {
		frame, more := runtimeFrames.Next()
		if frame.PC == 0 && !more {
			break
		}
		if frame.Function != "" || frame.File != "" {
			fmt.Fprintf(&b, "%s()\n\t%s:%d\n", frame.Function, frame.File, frame.Line)
		}
		if !more {
			break
		}
	}

	return b.String()
}

// parseGoStackTrace parses the output of runtime.Stack() into structured frames.
//
// Go stack traces use a two-line-per-frame format:
//
//	package.Function(args)
//		/absolute/path/to/file.go:42 +0x1a3
func parseGoStackTrace(stack string) []map[string]any {
	lines := strings.Split(stack, "\n")
	var frames []map[string]any

	// Skip the header line(s): "goroutine N [status]:"
	i := 0
	for i < len(lines) {
		if strings.HasPrefix(lines[i], "goroutine ") {
			i++
			continue
		}
		break
	}

	for i+1 < len(lines) {
		funcLine := strings.TrimSpace(lines[i])
		fileLine := strings.TrimSpace(lines[i+1])

		if funcLine == "" || fileLine == "" {
			i++
			continue
		}

		// fileLine should start with a path (after TrimSpace it starts with /)
		// Format: /path/to/file.go:42 +0x1a3
		if !looksLikeFileLine(fileLine) {
			i++
			continue
		}

		funcName := parseFuncName(funcLine)
		if shouldSkipFrame(funcName) {
			i += 2
			continue
		}
		absPath, lineno := parseFileLine(fileLine)

		frame := map[string]any{
			"function": funcOrDefault(funcName),
			"filename": makeRelativePath(absPath),
			"abs_path": absPath,
			"lineno":   lineno,
			"in_app":   isInApp(funcName, absPath),
		}
		frames = append(frames, frame)

		i += 2
	}

	return frames
}

// looksLikeFileLine returns true if the line appears to be a Go stack file reference.
func looksLikeFileLine(line string) bool {
	// After trimming, Go file lines contain a colon with a line number.
	// They typically start with / (Unix) or a drive letter (Windows).
	return strings.ContainsRune(line, ':')
}

// parseFuncName extracts the function name from a Go stack trace function line,
// stripping the argument list.
func parseFuncName(line string) string {
	// Function line format: "package.Function(args)" or "package.(*Type).Method(args)"
	if idx := strings.LastIndex(line, "("); idx > 0 {
		// Check if this is a method receiver like (*Type) by looking for matching parens
		candidate := line[:idx]
		if candidate != "" {
			return candidate
		}
	}
	return line
}

// parseFileLine extracts the absolute file path and line number from a Go stack
// trace file line. Format: "/path/to/file.go:42 +0x1a3" or "/path/to/file.go:42"
func parseFileLine(line string) (absPath string, lineno int) {
	// Strip the optional " +0x..." offset suffix
	if idx := strings.LastIndex(line, " +0x"); idx > 0 {
		line = line[:idx]
	}

	// Split on the last colon to separate path from line number
	lastColon := strings.LastIndex(line, ":")
	if lastColon < 0 {
		return line, 0
	}

	absPath = line[:lastColon]
	if n, err := strconv.Atoi(line[lastColon+1:]); err == nil {
		lineno = n
	}
	return absPath, lineno
}

func extractPackage(funcName string) string {
	if funcName == "" {
		return ""
	}
	if idx := strings.Index(funcName, ".("); idx > 0 {
		return funcName[:idx]
	}
	if idx := strings.LastIndex(funcName, "."); idx > 0 {
		return funcName[:idx]
	}
	return funcName
}

func shouldSkipFrame(funcName string) bool {
	pkg := extractPackage(funcName)

	if pkg == "runtime" || strings.HasPrefix(pkg, "runtime/") || pkg == "testing" {
		return true
	}

	if strings.HasPrefix(pkg, "go.agentcat.com/sdk") &&
		!strings.HasSuffix(pkg, "_test") {
		return true
	}

	return false
}

// isInApp determines whether a stack frame represents user/application code (true)
// or library/runtime code (false).
func isInApp(funcName string, filePath string) bool {
	normalizedPath := strings.ReplaceAll(filePath, "\\", "/")

	if goRoot != "" && strings.HasPrefix(normalizedPath, goRoot) {
		return false
	}

	pkg := extractPackage(funcName)
	if strings.Contains(pkg, "/vendor/") || strings.Contains(pkg, "/third_party/") {
		return false
	}

	return true
}

// makeRelativePath normalizes an absolute file path into a shorter relative path
// suitable for consistent error grouping across environments.
func makeRelativePath(absPath string) string {
	if absPath == "" || absPath == unknownFile {
		return absPath
	}

	result := absPath

	// Strip GOROOT prefix
	if goroot := runtime.GOROOT(); goroot != "" && strings.HasPrefix(result, goroot) {
		result = strings.TrimPrefix(result, goroot)
		result = strings.TrimPrefix(result, "/src/")
		result = strings.TrimPrefix(result, "/")
		return result
	}

	// Strip GOPATH prefix (can have multiple entries)
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		for _, p := range strings.Split(gopath, string(os.PathListSeparator)) {
			src := p + "/src/"
			if strings.HasPrefix(result, src) {
				return strings.TrimPrefix(result, src)
			}
			mod := p + "/pkg/mod/"
			if strings.HasPrefix(result, mod) {
				return strings.TrimPrefix(result, mod)
			}
		}
	}

	// Strip home directory prefixes
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(result, home) {
		result = "~" + strings.TrimPrefix(result, home)
	}

	// Strip common deployment prefixes
	deploymentPrefixes := []string{
		"/var/www/", "/var/task/", "/usr/src/app/",
		"/app/", "/opt/", "/srv/",
	}
	for _, prefix := range deploymentPrefixes {
		if strings.HasPrefix(result, prefix) {
			result = strings.TrimPrefix(result, prefix)
			break
		}
	}

	// Remove leading slash for clean relative paths
	result = strings.TrimPrefix(result, "/")

	return result
}

// unwrapErrorChain walks the Unwrap chain and returns chained error data.
func unwrapErrorChain(err error) []map[string]any {
	var chain []map[string]any
	seen := make(map[error]bool)
	current := errors.Unwrap(err)
	depth := 0

	for current != nil && depth < maxChainDepth {
		if seen[current] {
			break
		}
		seen[current] = true

		entry := map[string]any{
			"message": truncateMessage(current.Error()),
			"type":    fmt.Sprintf("%T", current),
		}

		if st, ok := current.(stackTracer); ok {
			frames := framesFromStackTracer(st)
			raw := formatStackTracerRaw(st)
			if raw != "" {
				entry["stack"] = raw
			}
			if len(frames) > 0 {
				entry["frames"] = truncateFrames(frames)
			}
		}

		chain = append(chain, entry)
		current = errors.Unwrap(current)
		depth++
	}

	return chain
}

func truncateMessage(msg string) string {
	if len(msg) <= maxMessageLength {
		return msg
	}
	return msg[:maxMessageLength-len(truncationSuffix)] + truncationSuffix
}

func truncateFrames(frames []map[string]any) []map[string]any {
	if len(frames) <= maxStackFrames {
		return frames
	}
	half := maxStackFrames / 2
	result := make([]map[string]any, 0, maxStackFrames)
	result = append(result, frames[:half]...)
	result = append(result, frames[len(frames)-half:]...)
	return result
}

func funcOrDefault(name string) string {
	if name == "" {
		return unknownFunction
	}
	return name
}
