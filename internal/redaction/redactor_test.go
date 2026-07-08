package redaction

import (
	"testing"

	agentcatapi "go.agentcat.com/api"
	"go.agentcat.com/sdk/internal/core"
)

func TestRedactEvent(t *testing.T) {
	tests := []struct {
		name      string
		event     *core.Event
		redactFn  core.RedactFunc
		want      *core.Event
		wantErr   bool
		errString string
	}{
		{
			name:     "nil event returns nil",
			event:    nil,
			redactFn: func(s string) string { return "REDACTED" },
			want:     nil,
			wantErr:  false,
		},
		{
			name:     "nil redact function returns nil",
			event:    &core.Event{},
			redactFn: nil,
			want:     &core.Event{},
			wantErr:  false,
		},
		{
			name: "redacts simple string in Parameters",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"key": "sensitive data",
					},
				},
			},
			redactFn: func(s string) string { return "***" },
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"key": "***",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "redacts simple string in Response",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Response: map[string]any{
						"message": "secret info",
					},
				},
			},
			redactFn: func(s string) string { return "[REDACTED]" },
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Response: map[string]any{
						"message": "[REDACTED]",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "redacts UserIntent",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					UserIntent: strPtr("show me my password"),
				},
			},
			redactFn: func(s string) string { return "show me my [REDACTED]" },
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					UserIntent: strPtr("show me my [REDACTED]"),
				},
			},
			wantErr: false,
		},
		{
			name: "redacts nested maps",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"outer": map[string]any{
							"inner": "nested secret",
						},
					},
				},
			},
			redactFn: func(s string) string { return "CLEAN" },
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"outer": map[string]any{
							"inner": "CLEAN",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "redacts slices",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"items": []any{"first", "second", "third"},
					},
				},
			},
			redactFn: func(s string) string { return "X" },
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"items": []any{"X", "X", "X"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "redacts complex nested structures",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"user": map[string]any{
							"name":  "john doe",
							"email": "john@example.com",
							"tags":  []any{"admin", "premium"},
						},
						"count":  42,
						"active": true,
					},
				},
			},
			redactFn: func(s string) string {
				// Simple email redaction
				if len(s) > 0 && s[0] == 'j' {
					return "***@***.com"
				}
				return s
			},
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"user": map[string]any{
							"name":  "***@***.com",
							"email": "***@***.com",
							"tags":  []any{"admin", "premium"},
						},
						"count":  42,
						"active": true,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "preserves non-string types",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"string": "text",
						"int":    123,
						"float":  45.67,
						"bool":   true,
						"nil":    nil,
					},
				},
			},
			redactFn: func(s string) string { return "REDACTED" },
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"string": "REDACTED",
						"int":    123,
						"float":  45.67,
						"bool":   true,
						"nil":    nil,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "handles panic in redact function",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"safe": "data",
					},
				},
			},
			redactFn: func(s string) string {
				panic("redaction error")
			},
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"safe": "[REDACTION_ERROR]",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "panic in redact function is recovered",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"key": "value",
					},
					Response: map[string]any{
						"result": "data",
					},
					UserIntent: strPtr("intent"),
				},
			},
			redactFn: func(s string) string {
				panic("catastrophic failure")
			},
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: map[string]any{
						"key": "[REDACTION_ERROR]",
					},
					Response: map[string]any{
						"result": "[REDACTION_ERROR]",
					},
					UserIntent: strPtr("[REDACTION_ERROR]"),
				},
			},
			wantErr: false,
		},
		{
			name: "empty UserIntent is not redacted",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					UserIntent: strPtr(""),
				},
			},
			redactFn: func(s string) string { return "REDACTED" },
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					UserIntent: strPtr(""),
				},
			},
			wantErr: false,
		},
		{
			name: "nil maps remain nil",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: nil,
					Response:   nil,
				},
			},
			redactFn: func(s string) string { return "REDACTED" },
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Parameters: nil,
					Response:   nil,
				},
			},
			wantErr: false,
		},
		{
			name: "redacts Error field strings",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Error: map[string]any{
						"message":  "connection to db://secret:pass@host failed",
						"type":     "*url.Error",
						"platform": "go",
						"stack":    "goroutine 1 [running]:\nmain.main()\n\t/app/main.go:10",
					},
				},
			},
			redactFn: func(s string) string { return "[REDACTED]" },
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Error: map[string]any{
						"message":  "[REDACTED]",
						"type":     "[REDACTED]",
						"platform": "[REDACTED]",
						"stack":    "[REDACTED]",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "nil Error map remains nil",
			event: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Error: nil,
				},
			},
			redactFn: func(s string) string { return "REDACTED" },
			want: &core.Event{
				PublishEventRequest: agentcatapi.PublishEventRequest{
					Error: nil,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RedactEvent(tt.event, tt.redactFn)

			// Check error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("RedactEvent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errString != "" {
				if err == nil || len(tt.errString) == 0 {
					t.Errorf("Expected error containing %q, got %v", tt.errString, err)
				}
			}

			// Skip comparison if event was nil
			if tt.event == nil {
				return
			}

			// Compare Parameters
			if !mapsEqual(tt.event.Parameters, tt.want.Parameters) {
				t.Errorf("Parameters mismatch:\ngot:  %+v\nwant: %+v", tt.event.Parameters, tt.want.Parameters)
			}

			// Compare Response
			if !mapsEqual(tt.event.Response, tt.want.Response) {
				t.Errorf("Response mismatch:\ngot:  %+v\nwant: %+v", tt.event.Response, tt.want.Response)
			}

			// Compare UserIntent
			if !strPtrEqual(tt.event.UserIntent, tt.want.UserIntent) {
				t.Errorf("UserIntent mismatch:\ngot:  %v\nwant: %v", ptrVal(tt.event.UserIntent), ptrVal(tt.want.UserIntent))
			}

			// Compare Error
			if !mapsEqual(tt.event.Error, tt.want.Error) {
				t.Errorf("Error mismatch:\ngot:  %+v\nwant: %+v", tt.event.Error, tt.want.Error)
			}
		})
	}
}

func TestRedactMap(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		redactFn core.RedactFunc
		want     map[string]any
	}{
		{
			name:     "nil map returns nil",
			input:    nil,
			redactFn: func(s string) string { return "REDACTED" },
			want:     nil,
		},
		{
			name:     "empty map returns empty map",
			input:    map[string]any{},
			redactFn: func(s string) string { return "REDACTED" },
			want:     map[string]any{},
		},
		{
			name: "creates deep copy",
			input: map[string]any{
				"key": "value",
			},
			redactFn: func(s string) string { return s + "_redacted" },
			want: map[string]any{
				"key": "value_redacted",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactMap(tt.input, tt.redactFn)

			if !mapsEqual(got, tt.want) {
				t.Errorf("redactMap() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRedactValue(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		redactFn core.RedactFunc
		want     any
	}{
		{
			name:     "nil value returns nil",
			input:    nil,
			redactFn: func(s string) string { return "X" },
			want:     nil,
		},
		{
			name:     "string gets redacted",
			input:    "secret",
			redactFn: func(s string) string { return "***" },
			want:     "***",
		},
		{
			name:     "int passes through",
			input:    42,
			redactFn: func(s string) string { return "X" },
			want:     42,
		},
		{
			name:     "float passes through",
			input:    3.14,
			redactFn: func(s string) string { return "X" },
			want:     3.14,
		},
		{
			name:     "bool passes through",
			input:    true,
			redactFn: func(s string) string { return "X" },
			want:     true,
		},
		{
			name: "nested map gets redacted",
			input: map[string]any{
				"key": "value",
			},
			redactFn: func(s string) string { return "R" },
			want: map[string]any{
				"key": "R",
			},
		},
		{
			name:     "slice gets redacted",
			input:    []any{"a", "b", "c"},
			redactFn: func(s string) string { return "X" },
			want:     []any{"X", "X", "X"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactValue(tt.input, tt.redactFn, make(map[uintptr]bool), maxRedactionDepth)

			if !valuesEqual(got, tt.want) {
				t.Errorf("redactValue() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSafeRedact(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		redactFn core.RedactFunc
		want     string
	}{
		{
			name:     "successful redaction",
			input:    "password123",
			redactFn: func(s string) string { return "***" },
			want:     "***",
		},
		{
			name:  "panic in redact function",
			input: "anything",
			redactFn: func(s string) string {
				panic("test panic")
			},
			want: "[REDACTION_ERROR]",
		},
		{
			name:     "empty string",
			input:    "",
			redactFn: func(s string) string { return "REDACTED" },
			want:     "REDACTED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safeRedact(tt.input, tt.redactFn)

			if got != tt.want {
				t.Errorf("safeRedact() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Helper functions

func strPtr(s string) *string {
	return &s
}

func ptrVal(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func strPtrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func mapsEqual(a, b map[string]any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a) != len(b) {
		return false
	}

	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		if !valuesEqual(va, vb) {
			return false
		}
	}

	return true
}

func valuesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	switch va := a.(type) {
	case string:
		vb, ok := b.(string)
		return ok && va == vb
	case int:
		vb, ok := b.(int)
		return ok && va == vb
	case float64:
		vb, ok := b.(float64)
		return ok && va == vb
	case bool:
		vb, ok := b.(bool)
		return ok && va == vb
	case map[string]any:
		vb, ok := b.(map[string]any)
		return ok && mapsEqual(va, vb)
	case []any:
		vb, ok := b.([]any)
		if !ok || len(va) != len(vb) {
			return false
		}
		for i := range va {
			if !valuesEqual(va[i], vb[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
