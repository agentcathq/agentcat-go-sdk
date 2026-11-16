package mcpgo

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"unsafe"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentcat "go.agentcat.com/sdk/v2"
)

// maybeStartServerInfo runs the server info heartbeat check for one handled
// request on the server c captures. When a heartbeat is due it starts on its
// own goroutine and sends the four list requests through the server's own
// message entry point, so the tool list is the one a client gets.
func maybeStartServerInfo(ctx context.Context, trigger agentcat.ServerInfoTrigger, c *capturer) {
	if c == nil {
		return
	}
	agentcat.MaybeStartServerInfo(ctx, trigger, agentcat.GetInstance(c.server), c.serverName,
		func() (*agentcat.ServerInfoSource, error) {
			return serverInfoSource(c), nil
		})
}

// serverInfoSource describes the server c captures for the heartbeat. A list
// whose capability the server did not register answers with an error, so it
// has no key in the body.
func serverInfoSource(c *capturer) *agentcat.ServerInfoSource {
	title, instructions := getServerTitleAndInstructions(c.server)
	return &agentcat.ServerInfoSource{
		ServerVersion:     c.serverVersion,
		ServerTitle:       title,
		Instructions:      instructions,
		Tools:             messageList(c.server, mcp.MethodToolsList, "tools"),
		Resources:         messageList(c.server, mcp.MethodResourcesList, "resources"),
		ResourceTemplates: messageList(c.server, mcp.MethodResourcesTemplatesList, "resourceTemplates"),
		Prompts:           messageList(c.server, mcp.MethodPromptsList, "prompts"),
	}
}

// serverInfoRequestID is the JSON-RPC id of the heartbeat's own list requests.
const serverInfoRequestID = "agentcat-server-info"

// messageList sends one page request of a list method through HandleMessage
// and reads the items under key from the result.
func messageList(s *server.MCPServer, method mcp.MCPMethod, key string) agentcat.ServerInfoListFunc {
	return func(ctx context.Context, cursor string) (agentcat.ServerInfoPage, error) {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := json.Marshal(map[string]any{
			"jsonrpc": mcp.JSONRPC_VERSION,
			"id":      serverInfoRequestID,
			"method":  method,
			"params":  params,
		})
		if err != nil {
			return agentcat.ServerInfoPage{}, err
		}
		answer, err := json.Marshal(s.HandleMessage(ctx, raw))
		if err != nil {
			return agentcat.ServerInfoPage{}, err
		}
		var reply struct {
			Result map[string]json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(answer, &reply); err != nil {
			return agentcat.ServerInfoPage{}, err
		}
		if reply.Error != nil {
			return agentcat.ServerInfoPage{}, fmt.Errorf("%s: %s (code %d)", method, reply.Error.Message, reply.Error.Code)
		}
		if reply.Result == nil {
			return agentcat.ServerInfoPage{}, fmt.Errorf("%s: no result", method)
		}
		var items []json.RawMessage
		if list, ok := reply.Result[key]; ok {
			if err := json.Unmarshal(list, &items); err != nil {
				return agentcat.ServerInfoPage{}, fmt.Errorf("%s: %w", method, err)
			}
		}
		var next string
		if cursor, ok := reply.Result["nextCursor"]; ok {
			if err := json.Unmarshal(cursor, &next); err != nil {
				return agentcat.ServerInfoPage{}, fmt.Errorf("%s: %w", method, err)
			}
		}
		page := agentcat.ServerInfoPage{Items: make([]any, len(items)), NextCursor: next}
		for i, item := range items {
			page.Items[i] = item
		}
		return page, nil
	}
}

// getServerTitleAndInstructions reads the MCPServer's unexported
// implementation and instructions fields reflectively, like getServerInfo:
// mcp-go v0.57.0 exposes no accessor for either.
func getServerTitleAndInstructions(s *server.MCPServer) (title, instructions string) {
	defer func() {
		if r := recover(); r != nil {
			title, instructions = "", ""
		}
	}()
	if s == nil {
		return "", ""
	}
	v := reflect.ValueOf(s).Elem()
	if f := v.FieldByName("implementation"); f.IsValid() {
		if impl, ok := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Interface().(mcp.Implementation); ok {
			title = impl.Title
		}
	}
	if f := v.FieldByName("instructions"); f.IsValid() && f.Kind() == reflect.String {
		instructions = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().String()
	}
	return title, instructions
}
