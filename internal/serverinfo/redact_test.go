package serverinfo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func replaceSecret(s string) string { return strings.ReplaceAll(s, "secret", "[x]") }

func TestRedact_V11_TextIsRedactedNamesAreNot(t *testing.T) {
	target := Target{ProjectID: demoTarget.ProjectID, ServerName: "secret-server", Redact: replaceSecret}
	src := &Source{
		ServerTitle:  "secret title",
		Instructions: "the secret plan",
		Tools: pages(10, map[string]any{
			"name":        "secret_tool",
			"description": "secret tool",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"secret": map[string]any{"type": "string", "description": "a secret"},
				},
			},
		}),
		Resources:         pages(10, map[string]any{"uri": "file:///secret.txt", "name": "secret", "description": "secret file"}),
		ResourceTemplates: pages(10, map[string]any{"uriTemplate": "file:///secret/{id}", "name": "t"}),
	}
	body, err := buildBody(context.Background(), target, src)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	if got := body.GetInstructions(); got != "the [x] plan" {
		t.Errorf("instructions = %q", got)
	}
	if got := body.GetServerTitle(); got != "[x] title" {
		t.Errorf("server_title = %q", got)
	}
	if got := body.GetServerName(); got != "secret-server" {
		t.Errorf("server_name = %q, want it unredacted", got)
	}
	tool := body.Features[KeyTools].([]any)[0].(map[string]any)
	if tool["name"] != "secret_tool" {
		t.Errorf("tool name = %v, want it unredacted", tool["name"])
	}
	if tool["description"] != "[x] tool" {
		t.Errorf("tool description = %v", tool["description"])
	}
	props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
	prop, ok := props["secret"].(map[string]any)
	if !ok {
		t.Fatalf("schema property key changed: %v", props)
	}
	if prop["description"] != "a [x]" {
		t.Errorf("schema description = %v", prop["description"])
	}
	resource := body.Features[KeyResources].([]any)[0].(map[string]any)
	if resource["uri"] != "file:///secret.txt" || resource["name"] != "secret" {
		t.Errorf("resource identity redacted: %v", resource)
	}
	if resource["description"] != "[x] file" {
		t.Errorf("resource description = %v", resource["description"])
	}
	template := body.Features[KeyResourceTemplates].([]any)[0].(map[string]any)
	if template["uriTemplate"] != "file:///secret/{id}" {
		t.Errorf("uriTemplate redacted: %v", template["uriTemplate"])
	}
}

func TestRedact_V12_FailingFunctionSendsNothing(t *testing.T) {
	target := demoTarget
	target.Redact = func(string) string { panic("redaction exploded") }
	src := &Source{Instructions: "x", Tools: pages(10, item("name", "alpha", "description", "d"))}
	if _, err := buildBody(context.Background(), target, src); err == nil {
		t.Fatal("buildBody succeeded, want an error when the redaction function fails")
	}
}

func TestRedact_NoFunctionKeepsTheText(t *testing.T) {
	src := &Source{Instructions: "the secret plan", Tools: pages(10, item("name", "a", "description", "secret"))}
	body, err := buildBody(context.Background(), demoTarget, src)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "the secret plan") {
		t.Fatalf("body = %s, want the text unchanged", raw)
	}
}
