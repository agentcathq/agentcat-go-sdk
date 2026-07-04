package mcpgo

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestAddContextParamsToToolsList(t *testing.T) {
	tests := []struct {
		name     string
		input    *mcp.ListToolsResult
		validate func(t *testing.T, result *mcp.ListToolsResult)
	}{
		{
			name:  "nil result",
			input: nil,
			validate: func(t *testing.T, result *mcp.ListToolsResult) {
				// Should not panic
			},
		},
		{
			name:  "empty tools array",
			input: &mcp.ListToolsResult{Tools: []mcp.Tool{}},
			validate: func(t *testing.T, result *mcp.ListToolsResult) {
				if len(result.Tools) != 0 {
					t.Errorf("Expected empty tools array, got %d tools", len(result.Tools))
				}
			},
		},
		{
			name: "single tool without context param",
			input: &mcp.ListToolsResult{
				Tools: []mcp.Tool{
					{
						Name:        "test_tool",
						Description: "A test tool",
						InputSchema: mcp.ToolInputSchema{
							Type:       "object",
							Properties: map[string]any{},
						},
					},
				},
			},
			validate: func(t *testing.T, result *mcp.ListToolsResult) {
				if len(result.Tools) != 1 {
					t.Fatalf("Expected 1 tool, got %d", len(result.Tools))
				}
				tool := result.Tools[0]
				if _, ok := tool.InputSchema.Properties[contextParamName]; !ok {
					t.Error("Context param was not added")
				}
				if !containsString(tool.InputSchema.Required, contextParamName) {
					t.Error("Context param not in required array")
				}
			},
		},
		{
			name: "single tool with context param already",
			input: &mcp.ListToolsResult{
				Tools: []mcp.Tool{
					{
						Name:        "test_tool",
						Description: "A test tool",
						InputSchema: mcp.ToolInputSchema{
							Type: "object",
							Properties: map[string]any{
								contextParamName: map[string]any{
									"type":        "string",
									"description": "existing",
								},
							},
							Required: []string{contextParamName},
						},
					},
				},
			},
			validate: func(t *testing.T, result *mcp.ListToolsResult) {
				if len(result.Tools) != 1 {
					t.Fatalf("Expected 1 tool, got %d", len(result.Tools))
				}
				tool := result.Tools[0]
				prop := tool.InputSchema.Properties[contextParamName].(map[string]any)
				if prop["description"] != "existing" {
					t.Error("Existing context param was modified")
				}
			},
		},
		{
			name: "multiple tools mixed",
			input: &mcp.ListToolsResult{
				Tools: []mcp.Tool{
					{
						Name:        "tool1",
						Description: "Tool without context",
						InputSchema: mcp.ToolInputSchema{
							Type:       "object",
							Properties: map[string]any{},
						},
					},
					{
						Name:        "tool2",
						Description: "Tool with context",
						InputSchema: mcp.ToolInputSchema{
							Type: "object",
							Properties: map[string]any{
								contextParamName: map[string]any{
									"type": "string",
								},
							},
						},
					},
				},
			},
			validate: func(t *testing.T, result *mcp.ListToolsResult) {
				if len(result.Tools) != 2 {
					t.Fatalf("Expected 2 tools, got %d", len(result.Tools))
				}
				for i, tool := range result.Tools {
					if _, ok := tool.InputSchema.Properties[contextParamName]; !ok {
						t.Errorf("Tool %d missing context param", i)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addContextParamsToToolsList(tt.input)
			tt.validate(t, tt.input)
		})
	}
}

func TestEnsureToolHasContextParam(t *testing.T) {
	tests := []struct {
		name     string
		input    mcp.Tool
		validate func(t *testing.T, result mcp.Tool)
	}{
		{
			name: "tool already has context param via InputSchema",
			input: mcp.Tool{
				Name: "test",
				InputSchema: mcp.ToolInputSchema{
					Type: "object",
					Properties: map[string]any{
						contextParamName: map[string]any{"type": "string"},
					},
				},
			},
			validate: func(t *testing.T, result mcp.Tool) {
				if _, ok := result.InputSchema.Properties[contextParamName]; !ok {
					t.Error("Context param was removed")
				}
			},
		},
		{
			name: "tool already has context param via RawInputSchema",
			input: mcp.Tool{
				Name: "test",
				RawInputSchema: json.RawMessage(`{
					"type": "object",
					"properties": {
						"context": {"type": "string"}
					}
				}`),
			},
			validate: func(t *testing.T, result mcp.Tool) {
				if len(result.RawInputSchema) == 0 {
					t.Error("RawInputSchema was cleared")
				}
			},
		},
		{
			name: "tool with RawInputSchema valid JSON",
			input: mcp.Tool{
				Name: "test",
				RawInputSchema: json.RawMessage(`{
					"type": "object",
					"properties": {
						"param1": {"type": "string"}
					}
				}`),
			},
			validate: func(t *testing.T, result mcp.Tool) {
				var schema map[string]any
				if err := json.Unmarshal(result.RawInputSchema, &schema); err != nil {
					t.Fatalf("Invalid JSON in result: %v", err)
				}
				props := schema["properties"].(map[string]any)
				if _, ok := props[contextParamName]; !ok {
					t.Error("Context param not added to RawInputSchema")
				}
				if _, ok := props["param1"]; !ok {
					t.Error("Existing property was removed")
				}
			},
		},
		{
			name: "tool with RawInputSchema invalid JSON",
			input: mcp.Tool{
				Name:           "test",
				RawInputSchema: json.RawMessage(`{invalid json`),
			},
			validate: func(t *testing.T, result mcp.Tool) {
				if string(result.RawInputSchema) != `{invalid json` {
					t.Error("Invalid JSON was modified")
				}
			},
		},
		{
			name: "tool with non-object InputSchema type",
			input: mcp.Tool{
				Name: "test",
				InputSchema: mcp.ToolInputSchema{
					Type: "string",
				},
			},
			validate: func(t *testing.T, result mcp.Tool) {
				if result.InputSchema.Type != "string" {
					t.Error("Type was modified")
				}
				if len(result.InputSchema.Properties) > 0 {
					t.Error("Properties were added to non-object type")
				}
			},
		},
		{
			name: "tool with empty InputSchema",
			input: mcp.Tool{
				Name:        "test",
				InputSchema: mcp.ToolInputSchema{},
			},
			validate: func(t *testing.T, result mcp.Tool) {
				if result.InputSchema.Type != "object" {
					t.Errorf("Type = %v, want 'object'", result.InputSchema.Type)
				}
				if _, ok := result.InputSchema.Properties[contextParamName]; !ok {
					t.Error("Context param not added")
				}
				if !containsString(result.InputSchema.Required, contextParamName) {
					t.Error("Context param not in required array")
				}
			},
		},
		{
			name: "tool with existing InputSchema properties",
			input: mcp.Tool{
				Name: "test",
				InputSchema: mcp.ToolInputSchema{
					Type: "object",
					Properties: map[string]any{
						"existingParam": map[string]any{"type": "number"},
					},
					Required: []string{"existingParam"},
				},
			},
			validate: func(t *testing.T, result mcp.Tool) {
				if _, ok := result.InputSchema.Properties["existingParam"]; !ok {
					t.Error("Existing property was removed")
				}
				if _, ok := result.InputSchema.Properties[contextParamName]; !ok {
					t.Error("Context param not added")
				}
				if !containsString(result.InputSchema.Required, "existingParam") {
					t.Error("Existing required field was removed")
				}
				if !containsString(result.InputSchema.Required, contextParamName) {
					t.Error("Context param not in required array")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ensureToolHasContextParam(tt.input)
			tt.validate(t, result)
		})
	}
}

func TestToolHasContextParam(t *testing.T) {
	tests := []struct {
		name  string
		input mcp.Tool
		want  bool
	}{
		{
			name: "context param in InputSchema.Properties",
			input: mcp.Tool{
				InputSchema: mcp.ToolInputSchema{
					Properties: map[string]any{
						contextParamName: map[string]any{"type": "string"},
					},
				},
			},
			want: true,
		},
		{
			name: "context param in RawInputSchema",
			input: mcp.Tool{
				RawInputSchema: json.RawMessage(`{
					"properties": {
						"context": {"type": "string"}
					}
				}`),
			},
			want: true,
		},
		{
			name: "context param doesn't exist",
			input: mcp.Tool{
				InputSchema: mcp.ToolInputSchema{
					Properties: map[string]any{
						"otherParam": map[string]any{"type": "string"},
					},
				},
			},
			want: false,
		},
		{
			name: "nil InputSchema.Properties",
			input: mcp.Tool{
				InputSchema: mcp.ToolInputSchema{},
			},
			want: false,
		},
		{
			name: "invalid RawInputSchema JSON",
			input: mcp.Tool{
				RawInputSchema: json.RawMessage(`{invalid`),
			},
			want: false,
		},
		{
			name:  "empty tool",
			input: mcp.Tool{},
			want:  false,
		},
		{
			name: "RawInputSchema without properties",
			input: mcp.Tool{
				RawInputSchema: json.RawMessage(`{"type": "object"}`),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolHasContextParam(tt.input)
			if got != tt.want {
				t.Errorf("toolHasContextParam() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAddContextParamToRawSchema(t *testing.T) {
	tests := []struct {
		name       string
		input      json.RawMessage
		wantOk     bool
		wantChange bool
		validate   func(t *testing.T, result json.RawMessage)
	}{
		{
			name:       "valid schema without context param",
			input:      json.RawMessage(`{"type": "object", "properties": {"param1": {"type": "string"}}}`),
			wantOk:     true,
			wantChange: true,
			validate: func(t *testing.T, result json.RawMessage) {
				var schema map[string]any
				if err := json.Unmarshal(result, &schema); err != nil {
					t.Fatalf("Failed to unmarshal result: %v", err)
				}
				props := schema["properties"].(map[string]any)
				if _, ok := props[contextParamName]; !ok {
					t.Error("Context param not added")
				}
				if _, ok := props["param1"]; !ok {
					t.Error("Existing property removed")
				}
				required := schema["required"].([]any)
				found := false
				for _, r := range required {
					if r.(string) == contextParamName {
						found = true
						break
					}
				}
				if !found {
					t.Error("Context param not in required array")
				}
			},
		},
		{
			name:       "schema already has context param",
			input:      json.RawMessage(`{"properties": {"context": {"type": "string"}}}`),
			wantOk:     false,
			wantChange: false,
			validate: func(t *testing.T, result json.RawMessage) {
				if string(result) != `{"properties": {"context": {"type": "string"}}}` {
					t.Error("Schema was modified despite having context param")
				}
			},
		},
		{
			name:       "invalid JSON",
			input:      json.RawMessage(`{invalid`),
			wantOk:     false,
			wantChange: false,
			validate: func(t *testing.T, result json.RawMessage) {
				if string(result) != `{invalid` {
					t.Error("Invalid JSON was modified")
				}
			},
		},
		{
			name:       "schema without properties field",
			input:      json.RawMessage(`{"type": "object"}`),
			wantOk:     true,
			wantChange: true,
			validate: func(t *testing.T, result json.RawMessage) {
				var schema map[string]any
				if err := json.Unmarshal(result, &schema); err != nil {
					t.Fatalf("Failed to unmarshal result: %v", err)
				}
				props := schema["properties"].(map[string]any)
				if _, ok := props[contextParamName]; !ok {
					t.Error("Context param not added")
				}
			},
		},
		{
			name:       "schema without type field",
			input:      json.RawMessage(`{"properties": {"param1": {"type": "string"}}}`),
			wantOk:     true,
			wantChange: true,
			validate: func(t *testing.T, result json.RawMessage) {
				var schema map[string]any
				if err := json.Unmarshal(result, &schema); err != nil {
					t.Fatalf("Failed to unmarshal result: %v", err)
				}
				if schema["type"] != "object" {
					t.Errorf("Type = %v, want 'object'", schema["type"])
				}
			},
		},
		{
			name:       "schema with existing required array",
			input:      json.RawMessage(`{"properties": {"param1": {"type": "string"}}, "required": ["param1"]}`),
			wantOk:     true,
			wantChange: true,
			validate: func(t *testing.T, result json.RawMessage) {
				var schema map[string]any
				if err := json.Unmarshal(result, &schema); err != nil {
					t.Fatalf("Failed to unmarshal result: %v", err)
				}
				required := schema["required"].([]any)
				foundParam1 := false
				foundContext := false
				for _, r := range required {
					if r.(string) == "param1" {
						foundParam1 = true
					}
					if r.(string) == contextParamName {
						foundContext = true
					}
				}
				if !foundParam1 {
					t.Error("Existing required field was removed")
				}
				if !foundContext {
					t.Error("Context param not added to required array")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := addContextParamToRawSchema(tt.input)
			if ok != tt.wantOk {
				t.Errorf("ok = %v, want %v", ok, tt.wantOk)
			}
			if tt.wantChange && string(result) == string(tt.input) {
				t.Error("Schema was not changed")
			}
			if !tt.wantChange && string(result) != string(tt.input) {
				t.Error("Schema was changed unexpectedly")
			}
			tt.validate(t, result)
		})
	}
}

func TestExtractRequired(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  []string
	}{
		{
			name:  "input is []string",
			input: []string{"param1", "param2"},
			want:  []string{"param1", "param2"},
		},
		{
			name:  "input is []any with strings",
			input: []any{"param1", "param2", "param3"},
			want:  []string{"param1", "param2", "param3"},
		},
		{
			name:  "input is []any with mixed types",
			input: []any{"param1", 123, "param2", true, "param3"},
			want:  []string{"param1", "param2", "param3"},
		},
		{
			name:  "input is nil",
			input: nil,
			want:  nil,
		},
		{
			name:  "input is other type",
			input: "not a slice",
			want:  nil,
		},
		{
			name:  "empty []string",
			input: []string{},
			want:  nil,
		},
		{
			name:  "empty []any",
			input: []any{},
			want:  []string{},
		},
		{
			name:  "[]any with no strings",
			input: []any{123, true, 45.6},
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRequired(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractRequired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCopyStringAnyMap(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]any
		want  map[string]any
	}{
		{
			name:  "nil input",
			input: nil,
			want:  nil,
		},
		{
			name:  "empty map",
			input: map[string]any{},
			want:  nil,
		},
		{
			name: "non-empty map",
			input: map[string]any{
				"key1": "value1",
				"key2": 42,
				"key3": true,
			},
			want: map[string]any{
				"key1": "value1",
				"key2": 42,
				"key3": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := copyStringAnyMap(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("copyStringAnyMap() = %v, want %v", got, tt.want)
			}
			// Verify it's a copy, not the same reference
			if len(tt.input) > 0 && got != nil {
				got["newKey"] = "newValue"
				if _, exists := tt.input["newKey"]; exists {
					t.Error("Original map was modified")
				}
			}
		})
	}
}

func TestCloneStringSlice(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "nil input",
			input: nil,
			want:  nil,
		},
		{
			name:  "empty slice",
			input: []string{},
			want:  nil,
		},
		{
			name:  "non-empty slice",
			input: []string{"a", "b", "c"},
			want:  []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cloneStringSlice(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("cloneStringSlice() = %v, want %v", got, tt.want)
			}
			if len(tt.input) > 0 && got != nil {
				got[0] = "modified"
				if tt.input[0] == "modified" {
					t.Error("Original slice was modified")
				}
			}
		})
	}
}

func TestEnsureStringPresent(t *testing.T) {
	tests := []struct {
		name      string
		input     []string
		candidate string
		want      []string
	}{
		{
			name:      "string exists",
			input:     []string{"a", "b", "c"},
			candidate: "b",
			want:      []string{"a", "b", "c"},
		},
		{
			name:      "string missing",
			input:     []string{"a", "b"},
			candidate: "c",
			want:      []string{"a", "b", "c"},
		},
		{
			name:      "nil slice",
			input:     nil,
			candidate: "a",
			want:      []string{"a"},
		},
		{
			name:      "empty slice",
			input:     []string{},
			candidate: "a",
			want:      []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureStringPresent(tt.input, tt.candidate)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ensureStringPresent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContainsString(t *testing.T) {
	tests := []struct {
		name   string
		list   []string
		target string
		want   bool
	}{
		{
			name:   "string exists",
			list:   []string{"a", "b", "c"},
			target: "b",
			want:   true,
		},
		{
			name:   "string missing",
			list:   []string{"a", "b", "c"},
			target: "d",
			want:   false,
		},
		{
			name:   "empty slice",
			list:   []string{},
			target: "a",
			want:   false,
		},
		{
			name:   "nil slice",
			list:   nil,
			target: "a",
			want:   false,
		},
		{
			name:   "empty target",
			list:   []string{"a", "", "c"},
			target: "",
			want:   true,
		},
		{
			name:   "first element",
			list:   []string{"a", "b", "c"},
			target: "a",
			want:   true,
		},
		{
			name:   "last element",
			list:   []string{"a", "b", "c"},
			target: "c",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsString(tt.list, tt.target)
			if got != tt.want {
				t.Errorf("containsString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractRequiredIndependence(t *testing.T) {
	original := []string{"param1", "param2"}
	result := extractRequired(original)

	if result == nil {
		t.Fatal("extractRequired returned nil for valid input")
	}

	result[0] = "modified"

	if original[0] == "modified" {
		t.Error("Original slice was modified when result was changed")
	}
}

func TestEnsureStringPresentIndependence(t *testing.T) {
	original := []string{"a", "b"}
	result := ensureStringPresent(original, "c")

	if len(result) != 3 {
		t.Fatalf("Expected 3 elements, got %d", len(result))
	}

	if len(original) != 2 {
		t.Error("Original slice was modified")
	}

	result[0] = "modified"

	if len(original) > 0 && original[0] == "modified" {
		t.Error("Original slice was modified when result was changed")
	}
}
