package officialsdk

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	agentcat "go.agentcat.com/sdk"
)

func TestGetOrCreateSession_NilRequest(t *testing.T) {
	sessionMap := agentcat.NewSessionMap(0)
	defer sessionMap.Stop()
	ps := getOrCreateSession(nil, sessionMap, nil, "proj_123")
	if ps != nil {
		t.Error("expected nil session for nil request")
	}
}

func TestGetOrCreateSession_NilSession(t *testing.T) {
	req := &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name: "test",
		},
	}
	sessionMap := agentcat.NewSessionMap(0)
	defer sessionMap.Stop()
	ps := getOrCreateSession(req, sessionMap, nil, "proj_123")
	if ps != nil {
		t.Error("expected nil session when request has no Session set")
	}
}

func TestGetOrCreateSession_ServerImplValues(t *testing.T) {
	serverImpl := &mcp.Implementation{
		Name:    "my-server",
		Version: "v2.0.0",
	}

	if serverImpl.Name != "my-server" {
		t.Errorf("expected server name 'my-server', got '%s'", serverImpl.Name)
	}
	if serverImpl.Version != "v2.0.0" {
		t.Errorf("expected server version 'v2.0.0', got '%s'", serverImpl.Version)
	}
}
