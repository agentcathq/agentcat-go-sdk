package serverinfo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	agentcatapi "go.agentcat.com/api"
)

// Request is the body of one heartbeat.
type Request = agentcatapi.PublishServerInfoRequest

// The keys of the lists in the body's features, as the MCP specification
// names the list types.
const (
	KeyTools             = "tools"
	KeyResources         = "resources"
	KeyResourceTemplates = "resourceTemplates"
	KeyPrompts           = "prompts"
)

// Source is what an adapter knows about one server: the fields its library
// keeps, and a handler for each list. A nil ListFunc means the server has no
// handler for that list, and the body then has no key for it.
type Source struct {
	ServerVersion string
	ServerTitle   string
	Instructions  string

	Tools             ListFunc
	Resources         ListFunc
	ResourceTemplates ListFunc
	Prompts           ListFunc

	// FinishTools turns the joined tool pages into the list a client gets
	// from tools/list. Nil keeps the pages as they are.
	FinishTools func(tools []any) ([]any, error)

	// DropUnsupported removes the key of each list the server does not
	// advertise, after the lists are read. Nil keeps every list that was read.
	DropUnsupported func(features map[string][]any)
}

var errNoToolList = errors.New("the server has no tool list")

// buildBody calls the list handlers, runs the redaction function and returns
// the body. It fails when the tool list fails; another list that fails is
// left out of the body.
func buildBody(ctx context.Context, target Target, src *Source) (*Request, error) {
	features, err := readFeatures(ctx, src)
	if err != nil {
		return nil, err
	}

	title, instructions := src.ServerTitle, src.Instructions
	if target.Redact != nil {
		if title, err = redactText(target.Redact, title); err != nil {
			return nil, err
		}
		if instructions, err = redactText(target.Redact, instructions); err != nil {
			return nil, err
		}
		if err := redactFeatures(features, target.Redact); err != nil {
			return nil, err
		}
	}

	body := agentcatapi.NewPublishServerInfoRequest(target.ProjectID)
	body.SetServerName(target.ServerName)
	body.SetServerVersion(src.ServerVersion)
	body.SetServerTitle(title)
	body.SetInstructions(instructions)
	body.Features = make(map[string]any, len(features))
	for key, items := range features {
		body.Features[key] = items
	}
	return body, nil
}

func readFeatures(ctx context.Context, src *Source) (map[string][]any, error) {
	if src.Tools == nil {
		return nil, errNoToolList
	}
	features := map[string][]any{}

	tools, err := collectList(ctx, src.Tools)
	if err == nil && src.FinishTools != nil {
		tools, err = src.FinishTools(tools)
	}
	if err == nil {
		tools, err = toPlainItems(tools)
	}
	if err != nil {
		return nil, fmt.Errorf("tool list failed: %w", err)
	}
	features[KeyTools] = tools

	for _, list := range []struct {
		key string
		fn  ListFunc
	}{
		{KeyResources, src.Resources},
		{KeyResourceTemplates, src.ResourceTemplates},
		{KeyPrompts, src.Prompts},
	} {
		if list.fn == nil {
			continue
		}
		items, err := collectList(ctx, list.fn)
		if err == nil {
			items, err = toPlainItems(items)
		}
		if err != nil {
			logDebug("server info: %s list left out: %v", list.key, err)
			continue
		}
		features[list.key] = items
	}

	if src.DropUnsupported != nil {
		src.DropUnsupported(features)
	}
	if _, ok := features[KeyTools]; !ok {
		return nil, errNoToolList
	}
	return features, nil
}

// toPlainItems turns each item into the plain JSON value it marshals to, so
// the redaction walk sees only maps, slices, strings, numbers, booleans and
// nulls. Numbers keep their literal.
func toPlainItems(items []any) ([]any, error) {
	plain := make([]any, 0, len(items))
	for _, item := range items {
		value, err := toPlainJSON(item)
		if err != nil {
			return nil, err
		}
		plain = append(plain, value)
	}
	return plain, nil
}

func toPlainJSON(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// withoutToolSchemas returns a copy of body whose tools carry no inputSchema
// and no outputSchema. Every other field stays.
func withoutToolSchemas(body *Request) *Request {
	smaller := *body
	smaller.Features = make(map[string]any, len(body.Features))
	for key, value := range body.Features {
		smaller.Features[key] = value
	}
	tools, _ := body.Features[KeyTools].([]any)
	stripped := make([]any, 0, len(tools))
	for _, tool := range tools {
		obj, ok := tool.(map[string]any)
		if !ok {
			stripped = append(stripped, tool)
			continue
		}
		cp := make(map[string]any, len(obj))
		for key, value := range obj {
			if key == "inputSchema" || key == "outputSchema" {
				continue
			}
			cp[key] = value
		}
		stripped = append(stripped, cp)
	}
	smaller.Features[KeyTools] = stripped
	return &smaller
}
