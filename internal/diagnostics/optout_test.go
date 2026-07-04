package diagnostics

import (
	"strconv"
	"testing"

	"go.agentcat.com/sdk/internal/logging"
)

func TestInit_EnabledByDefault(t *testing.T) {
	// Force-enable: under go test the SDK auto-disables, so opt back in explicitly
	// to assert the default (non-test) enabled behavior.
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	ResetForTest()
	defer ResetForTest()

	Init("proj_x", false, "officialsdk", "github.com/modelcontextprotocol/go-sdk")
	if !Enabled() {
		t.Fatal("diagnostics must be enabled by default")
	}
}

func TestInit_DisabledViaOption(t *testing.T) {
	t.Setenv("DISABLE_DIAGNOSTICS", "")
	ResetForTest()
	defer ResetForTest()

	Init("proj_x", true, "officialsdk", "p")
	if Enabled() {
		t.Fatal("disabled=true must disable diagnostics")
	}
}

func TestInit_EnvDisableValues(t *testing.T) {
	disable := []string{"true", "TRUE", "1", "yes", "on"}
	for _, v := range disable {
		t.Run("disable_"+v, func(t *testing.T) {
			t.Setenv("DISABLE_DIAGNOSTICS", v)
			ResetForTest()
			defer ResetForTest()
			Init("p", false, "x", "y")
			if Enabled() {
				t.Errorf("%q must disable diagnostics", v)
			}
		})
	}

	// Explicit falsy values force-enable diagnostics even under go test.
	// (Whitespace-only is "unset", covered by the go-test auto-disable tests.)
	stay := []string{"false", "0", "no", "off"}
	for _, v := range stay {
		t.Run("enabled_"+v, func(t *testing.T) {
			t.Setenv("DISABLE_DIAGNOSTICS", v)
			ResetForTest()
			defer ResetForTest()
			Init("p", false, "x", "y")
			if !Enabled() {
				t.Errorf("%q must NOT disable diagnostics", v)
			}
		})
	}
}

func TestInit_RegistersSink(t *testing.T) {
	// Force-enable: the SDK auto-disables under go test.
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	ResetForTest()
	defer ResetForTest()

	Init("p", false, "x", "y")
	// The sink should be registered; capturing an Info entry must enqueue a record.
	logging.New().Info("a setup line")
	if n := bufferLenForTest(); n == 0 {
		t.Fatal("Init must register the sink so Info entries are captured")
	}
}

func TestCapture_IgnoresDebug(t *testing.T) {
	// Force-enable: the SDK auto-disables under go test.
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	ResetForTest()
	defer ResetForTest()

	Init("p", false, "x", "y")

	capture(logging.LevelDebug, "x")
	if n := bufferLenForTest(); n != 0 {
		t.Fatalf("debug entries must be ignored: got buffer len %d, want 0", n)
	}

	capture(logging.LevelInfo, "y")
	if n := bufferLenForTest(); n != 1 {
		t.Fatalf("info entries must be captured: got buffer len %d, want 1", n)
	}
}

func TestCapture_DropOldestAtMaxBuffer(t *testing.T) {
	// Force-enable: the SDK auto-disables under go test.
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	ResetForTest()
	defer ResetForTest()

	Init("p", false, "x", "y")

	for i := 0; i < maxBuffer+5; i++ {
		capture(logging.LevelInfo, "msg "+strconv.Itoa(i))
	}
	if n := bufferLenForTest(); n != maxBuffer {
		t.Fatalf("drop-oldest must cap the buffer: got len %d, want %d", n, maxBuffer)
	}
}

func TestInit_Idempotent(t *testing.T) {
	// Force-enable: the SDK auto-disables under go test.
	t.Setenv("DISABLE_DIAGNOSTICS", "false")
	ResetForTest()
	defer ResetForTest()

	Init("proj_a", false, "officialsdk", "github.com/modelcontextprotocol/go-sdk")
	// A second Init (different project, disabled=true, different integration) must
	// be a complete no-op — the first call wins for the whole process.
	Init("proj_b", true, "mcpgo", "github.com/mark3labs/mcp-go")

	if !Enabled() {
		t.Fatal("second Init must not flip enabled (idempotency)")
	}

	var projectID, integration string
	for _, a := range StaticAttributesForTest() {
		switch a.Key {
		case "mcpcat.project_id":
			projectID = a.Value.StringValue
		case "mcpcat.integration":
			integration = a.Value.StringValue
		}
	}
	if projectID != "proj_a" {
		t.Errorf("first Init must win: project_id = %q, want proj_a", projectID)
	}
	if integration != "officialsdk" {
		t.Errorf("first Init must win: integration = %q, want officialsdk", integration)
	}
}
