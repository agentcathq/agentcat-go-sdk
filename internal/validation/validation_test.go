package validation

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateTags(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]string
		want map[string]string
	}{
		{
			name: "nil input returns nil",
			in:   nil,
			want: nil,
		},
		{
			name: "empty input returns nil",
			in:   map[string]string{},
			want: nil,
		},
		{
			name: "valid tags pass through",
			in: map[string]string{
				"env":            "production",
				"trace_id":       "abc-123",
				"my.tag:v1":      "value",
				"$special- key ": "ok",
			},
			want: map[string]string{
				"env":            "production",
				"trace_id":       "abc-123",
				"my.tag:v1":      "value",
				"$special- key ": "ok",
			},
		},
		{
			name: "invalid key characters dropped",
			in: map[string]string{
				"good":     "v",
				"bad/key":  "v",
				"bad#key":  "v",
				"bad\nkey": "v",
			},
			want: map[string]string{"good": "v"},
		},
		{
			name: "empty key dropped",
			in:   map[string]string{"": "v", "ok": "v"},
			want: map[string]string{"ok": "v"},
		},
		{
			name: "key over 32 chars dropped",
			in: map[string]string{
				strings.Repeat("k", 33): "v",
				strings.Repeat("k", 32): "v",
			},
			want: map[string]string{strings.Repeat("k", 32): "v"},
		},
		{
			name: "value over 200 chars dropped",
			in: map[string]string{
				"long":  strings.Repeat("v", 201),
				"exact": strings.Repeat("v", 200),
			},
			want: map[string]string{"exact": strings.Repeat("v", 200)},
		},
		{
			name: "value with newline dropped",
			in:   map[string]string{"nl": "a\nb", "ok": "ab"},
			want: map[string]string{"ok": "ab"},
		},
		{
			name: "all invalid returns nil",
			in:   map[string]string{"bad/key": "v", "nl": "a\nb"},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateTags(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d entries, want %d: %v", len(got), len(tt.want), got)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("tag %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestValidateTags_MaxEntries(t *testing.T) {
	in := make(map[string]string, 60)
	for i := 0; i < 60; i++ {
		in[fmt.Sprintf("key%02d", i)] = "v"
	}

	got := ValidateTags(in)
	if len(got) != MaxTagEntries {
		t.Fatalf("got %d entries, want %d", len(got), MaxTagEntries)
	}

	// Deterministic cutoff: sorted key order keeps key00..key49.
	if _, ok := got["key00"]; !ok {
		t.Error("expected key00 to be kept")
	}
	if _, ok := got["key49"]; !ok {
		t.Error("expected key49 to be kept")
	}
	if _, ok := got["key50"]; ok {
		t.Error("expected key50 to be dropped")
	}
}

func TestValidateTags_DoesNotMutateInput(t *testing.T) {
	in := map[string]string{"ok": "v", "bad/key": "v"}
	_ = ValidateTags(in)
	if len(in) != 2 {
		t.Errorf("input map was mutated: %v", in)
	}
}
