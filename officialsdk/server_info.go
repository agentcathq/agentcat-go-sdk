package officialsdk

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"unsafe"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	agentcat "go.agentcat.com/sdk/v2"
)

// maybeStartServerInfo runs the server info heartbeat check for one handled
// request. When a heartbeat is due it starts on its own goroutine and calls
// next — the handler chain below AgentCat's middleware — for each list method
// with the request's live session.
func maybeStartServerInfo(
	ctx context.Context,
	trigger agentcat.ServerInfoTrigger,
	req mcp.Request,
	next mcp.MethodHandler,
	serverRef *mcp.Server,
	serverImpl *mcp.Implementation,
	hookMode bool,
) {
	instance := getMCPcat(serverRef)
	serverName := ""
	if serverImpl != nil {
		serverName = serverImpl.Name
	}
	agentcat.MaybeStartServerInfo(ctx, trigger, instance, serverName, func() (*agentcat.ServerInfoSource, error) {
		return serverInfoSource(req, next, serverRef, serverImpl, instance, hookMode)
	})
}

// serverInfoSource describes one server for the heartbeat. The tool pages
// come from the inner chain without AgentCat's changes, so FinishTools runs
// the same injection a tools/list response gets.
func serverInfoSource(
	req mcp.Request,
	next mcp.MethodHandler,
	serverRef *mcp.Server,
	serverImpl *mcp.Implementation,
	instance *agentcat.AgentCatInstance,
	hookMode bool,
) (*agentcat.ServerInfoSource, error) {
	session, _ := req.GetSession().(*mcp.ServerSession)
	if session == nil {
		return nil, errors.New("the request has no live server session")
	}
	extra := req.GetExtra()
	serverOpts := getServerOptions(serverRef)

	src := &agentcat.ServerInfoSource{
		Tools: listFunc(next, "tools/list",
			func(cursor string) mcp.Request {
				return &mcp.ListToolsRequest{Session: session, Params: &mcp.ListToolsParams{Cursor: cursor}, Extra: extra}
			},
			func(r *mcp.ListToolsResult) agentcat.ServerInfoPage { return page(r.Tools, r.NextCursor) }),
		Resources: listFunc(next, "resources/list",
			func(cursor string) mcp.Request {
				return &mcp.ListResourcesRequest{Session: session, Params: &mcp.ListResourcesParams{Cursor: cursor}, Extra: extra}
			},
			func(r *mcp.ListResourcesResult) agentcat.ServerInfoPage { return page(r.Resources, r.NextCursor) }),
		ResourceTemplates: listFunc(next, "resources/templates/list",
			func(cursor string) mcp.Request {
				return &mcp.ListResourceTemplatesRequest{Session: session, Params: &mcp.ListResourceTemplatesParams{Cursor: cursor}, Extra: extra}
			},
			func(r *mcp.ListResourceTemplatesResult) agentcat.ServerInfoPage {
				return page(r.ResourceTemplates, r.NextCursor)
			}),
		Prompts: listFunc(next, "prompts/list",
			func(cursor string) mcp.Request {
				return &mcp.ListPromptsRequest{Session: session, Params: &mcp.ListPromptsParams{Cursor: cursor}, Extra: extra}
			},
			func(r *mcp.ListPromptsResult) agentcat.ServerInfoPage { return page(r.Prompts, r.NextCursor) }),
		FinishTools: func(items []any) ([]any, error) {
			return injectListedTools(items, instance, hookMode)
		},
		DropUnsupported: func(features map[string][]any) {
			dropUnadvertisedLists(features, serverOpts)
		},
	}
	if serverImpl != nil {
		src.ServerVersion = serverImpl.Version
		src.ServerTitle = serverImpl.Title
	}
	if serverOpts != nil {
		src.Instructions = serverOpts.Instructions
	}
	return src, nil
}

// listFunc calls one list method on the inner chain and reads one page of its
// typed result.
func listFunc[R mcp.Result](
	next mcp.MethodHandler,
	method string,
	request func(cursor string) mcp.Request,
	read func(R) agentcat.ServerInfoPage,
) agentcat.ServerInfoListFunc {
	return func(ctx context.Context, cursor string) (agentcat.ServerInfoPage, error) {
		res, err := next(ctx, method, request(cursor))
		if err != nil {
			return agentcat.ServerInfoPage{}, err
		}
		typed, ok := res.(R)
		if !ok {
			return agentcat.ServerInfoPage{}, fmt.Errorf("%s returned %T", method, res)
		}
		return read(typed), nil
	}
}

func page[T any](items []T, nextCursor string) agentcat.ServerInfoPage {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return agentcat.ServerInfoPage{Items: out, NextCursor: nextCursor}
}

// injectListedTools applies the tools/list injection to the joined tool
// pages, on copies, so the heartbeat holds the list a client gets.
func injectListedTools(items []any, instance *agentcat.AgentCatInstance, hookMode bool) ([]any, error) {
	tools := make([]*mcp.Tool, 0, len(items))
	for _, item := range items {
		tool, ok := item.(*mcp.Tool)
		if !ok || tool == nil {
			return nil, fmt.Errorf("tools/list returned an item of type %T", item)
		}
		tools = append(tools, tool)
	}
	result := &mcp.ListToolsResult{Tools: tools}
	if instance != nil && instance.Options != nil {
		cfg := agentcat.BuildInjectConfig(instance.Options, hookMode)
		injected, _ := agentcat.BuildInjectedTools(cfg, normalizeOfficialTools(tools))
		applyInjectedTools(result, injected)
	}
	out := make([]any, len(result.Tools))
	for i, tool := range result.Tools {
		out[i] = tool
	}
	return out, nil
}

// dropUnadvertisedLists removes the lists a client is not offered. The go-sdk
// answers every list method, but advertises a capability only when the server
// has items of that kind or its options declare the capability, and a client
// never asks for a list the server does not advertise.
func dropUnadvertisedLists(features map[string][]any, opts *mcp.ServerOptions) {
	var caps *mcp.ServerCapabilities
	if opts != nil {
		caps = opts.Capabilities
	}
	declaresTools := opts != nil && (opts.HasTools || (caps != nil && caps.Tools != nil))
	declaresPrompts := opts != nil && (opts.HasPrompts || (caps != nil && caps.Prompts != nil))
	declaresResources := opts != nil && (opts.HasResources || (caps != nil && caps.Resources != nil))

	if !declaresTools && len(features[agentcat.ServerInfoKeyTools]) == 0 {
		delete(features, agentcat.ServerInfoKeyTools)
	}
	if !declaresPrompts && len(features[agentcat.ServerInfoKeyPrompts]) == 0 {
		delete(features, agentcat.ServerInfoKeyPrompts)
	}
	if !declaresResources && len(features[agentcat.ServerInfoKeyResources]) == 0 &&
		len(features[agentcat.ServerInfoKeyResourceTemplates]) == 0 {
		delete(features, agentcat.ServerInfoKeyResources)
		delete(features, agentcat.ServerInfoKeyResourceTemplates)
	}
}

// getServerOptions reads the server's unexported opts field reflectively, the
// same sanctioned private access as getServerImpl: the go-sdk keeps the
// instructions there and exposes no accessor. Returns a shallow copy, or nil
// when the layout changes.
func getServerOptions(mcpServer *mcp.Server) (opts *mcp.ServerOptions) {
	defer func() {
		if r := recover(); r != nil {
			opts = nil
		}
	}()
	v := reflect.ValueOf(mcpServer).Elem().FieldByName("opts")
	if !v.IsValid() {
		return nil
	}
	v = reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
	got, ok := v.Interface().(mcp.ServerOptions)
	if !ok {
		return nil
	}
	return &got
}
