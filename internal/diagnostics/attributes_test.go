package diagnostics

import "testing"

func attrMap(attrs []otlpAttribute) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[a.Key] = a.Value.StringValue
	}
	return m
}

func TestBuildStaticAttributes_WithProjectID(t *testing.T) {
	m := attrMap(buildStaticAttributes("proj_123", "officialsdk", "github.com/modelcontextprotocol/go-sdk"))

	if m["mcpcat.project_id"] != "proj_123" {
		t.Errorf("project_id = %q, want proj_123", m["mcpcat.project_id"])
	}
	if _, ok := m["mcpcat.install_id"]; ok {
		t.Error("install_id must be absent when project_id is set")
	}
	if m["mcpcat.sdk.language"] != "go" {
		t.Errorf("sdk.language = %q, want go", m["mcpcat.sdk.language"])
	}
	if m["mcpcat.integration"] != "officialsdk" {
		t.Errorf("integration = %q, want officialsdk", m["mcpcat.integration"])
	}
	if m["os.type"] == "" {
		t.Error("os.type must be present")
	}
	if m["host.arch"] == "" {
		t.Error("host.arch must be present")
	}
	if m["process.runtime.name"] != "go" {
		t.Errorf("process.runtime.name = %q, want go", m["process.runtime.name"])
	}
}

func TestBuildStaticAttributes_WithoutProjectID(t *testing.T) {
	m := attrMap(buildStaticAttributes("", "mcpgo", "github.com/mark3labs/mcp-go"))

	if _, ok := m["mcpcat.project_id"]; ok {
		t.Error("project_id must be absent when empty")
	}
	if m["mcpcat.install_id"] == "" {
		t.Error("install_id must be present (anonymous) when project_id is empty")
	}
	if len(m["mcpcat.install_id"]) != 16 {
		t.Errorf("install_id must be 16 hex chars, got %q", m["mcpcat.install_id"])
	}
}

func TestComputeInstallID_StableAndShort(t *testing.T) {
	a := computeInstallID()
	b := computeInstallID()
	if a != b {
		t.Errorf("install_id must be stable: %q != %q", a, b)
	}
	if len(a) != 16 {
		t.Errorf("install_id length = %d, want 16", len(a))
	}
}

func TestBuildStaticAttributes_DeploymentEnvironment(t *testing.T) {
	t.Setenv("ENVIRONMENT", "")
	if _, ok := attrMap(buildStaticAttributes("p", "x", "y"))["deployment.environment"]; ok {
		t.Error("deployment.environment must be omitted when ENVIRONMENT is unset")
	}
	t.Setenv("ENVIRONMENT", "production")
	if attrMap(buildStaticAttributes("p", "x", "y"))["deployment.environment"] != "production" {
		t.Error("deployment.environment must equal ENVIRONMENT")
	}
}
