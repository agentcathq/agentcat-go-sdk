package diagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"runtime"
	"strconv"

	"go.agentcat.com/sdk/v2/internal/core"
)

// computeInstallID returns a stable, anonymous 16-hex-char id derived from the
// hostname and executable path. Best-effort; empty inputs are tolerated.
func computeInstallID() string {
	hostname, _ := os.Hostname()
	exePath, _ := os.Executable()
	h := sha256.Sum256([]byte(hostname + "|" + exePath))
	return hex.EncodeToString(h[:])[:16]
}

// buildStaticAttributes builds the OTLP resource attributes (identity +
// environment metadata). Empty values are omitted entirely.
func buildStaticAttributes(projectID, integration, mcpSDKPath string) []otlpAttribute {
	var attrs []otlpAttribute
	add := func(key, value string) {
		if value != "" {
			attrs = append(attrs, otlpAttribute{Key: key, Value: otlpAttrValue{StringValue: value}})
		}
	}

	if projectID != "" {
		add("agentcat.project_id", projectID)
	} else {
		add("agentcat.install_id", computeInstallID())
	}

	add("agentcat.sdk.language", "go")
	add("agentcat.sdk.version", core.GetDependencyVersion(core.SDKModulePath))
	add("agentcat.mcp_sdk.version", core.GetDependencyVersion(mcpSDKPath))
	add("agentcat.integration", integration)

	add("process.runtime.name", "go")
	add("process.runtime.version", runtime.Version())
	add("process.pid", strconv.Itoa(os.Getpid()))

	add("os.type", runtime.GOOS)
	add("host.arch", runtime.GOARCH)
	add("host.cpu.count", strconv.Itoa(runtime.NumCPU()))

	add("deployment.environment", os.Getenv("ENVIRONMENT"))

	return attrs
}

// buildRecordVersionAttrs builds the version attributes stamped on every
// individual OTLP logRecord, mirroring the same keys in the resource block so
// record-level-only backend views still see them. Empty values are omitted.
func buildRecordVersionAttrs(sdkVersion, mcpVersion string) []otlpAttribute {
	var attrs []otlpAttribute
	add := func(key, value string) {
		if value != "" {
			attrs = append(attrs, otlpAttribute{Key: key, Value: otlpAttrValue{StringValue: value}})
		}
	}

	add("agentcat.sdk.version", sdkVersion)
	add("agentcat.mcp_sdk.version", mcpVersion)
	add("process.runtime.version", runtime.Version())

	return attrs
}
