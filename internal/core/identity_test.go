package core

import "testing"

func TestIdentitiesEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b *UserIdentity
		want bool
	}{
		{"both nil", nil, nil, true},
		{"one nil", &UserIdentity{UserID: "u1"}, nil, false},
		{
			"equal basic",
			&UserIdentity{UserID: "u1", UserName: "Alice"},
			&UserIdentity{UserID: "u1", UserName: "Alice"},
			true,
		},
		{
			"different user id",
			&UserIdentity{UserID: "u1"},
			&UserIdentity{UserID: "u2"},
			false,
		},
		{
			"different user name",
			&UserIdentity{UserID: "u1", UserName: "Alice"},
			&UserIdentity{UserID: "u1", UserName: "Bob"},
			false,
		},
		{
			"equal user data",
			&UserIdentity{UserID: "u1", UserData: map[string]any{"plan": "pro", "n": 1}},
			&UserIdentity{UserID: "u1", UserData: map[string]any{"plan": "pro", "n": 1}},
			true,
		},
		{
			"different user data value",
			&UserIdentity{UserID: "u1", UserData: map[string]any{"plan": "pro"}},
			&UserIdentity{UserID: "u1", UserData: map[string]any{"plan": "free"}},
			false,
		},
		{
			"different user data keys",
			&UserIdentity{UserID: "u1", UserData: map[string]any{"plan": "pro"}},
			&UserIdentity{UserID: "u1", UserData: map[string]any{"tier": "pro"}},
			false,
		},
		{
			"nested user data equal",
			&UserIdentity{UserID: "u1", UserData: map[string]any{"org": map[string]any{"id": "o1"}}},
			&UserIdentity{UserID: "u1", UserData: map[string]any{"org": map[string]any{"id": "o1"}}},
			true,
		},
		{
			"nil vs empty user data",
			&UserIdentity{UserID: "u1"},
			&UserIdentity{UserID: "u1", UserData: map[string]any{}},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IdentitiesEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("IdentitiesEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeIdentities(t *testing.T) {
	t.Run("nil previous returns next", func(t *testing.T) {
		next := &UserIdentity{UserID: "u1"}
		if got := MergeIdentities(nil, next); got != next {
			t.Errorf("expected next identity, got %+v", got)
		}
	})

	t.Run("nil next returns previous", func(t *testing.T) {
		prev := &UserIdentity{UserID: "u1"}
		if got := MergeIdentities(prev, nil); got != prev {
			t.Errorf("expected previous identity, got %+v", got)
		}
	})

	t.Run("overwrites id and name, merges data", func(t *testing.T) {
		prev := &UserIdentity{
			UserID:   "u1",
			UserName: "Alice",
			UserData: map[string]any{"plan": "free", "region": "us"},
		}
		next := &UserIdentity{
			UserID:   "u2",
			UserName: "Bob",
			UserData: map[string]any{"plan": "pro"},
		}

		got := MergeIdentities(prev, next)
		if got.UserID != "u2" || got.UserName != "Bob" {
			t.Errorf("id/name not overwritten: %+v", got)
		}
		if got.UserData["plan"] != "pro" {
			t.Errorf("next data should win: %v", got.UserData["plan"])
		}
		if got.UserData["region"] != "us" {
			t.Errorf("previous data should be preserved: %v", got.UserData["region"])
		}

		// Inputs must not be mutated.
		if prev.UserData["plan"] != "free" {
			t.Error("previous identity was mutated")
		}
		if len(next.UserData) != 1 {
			t.Error("next identity was mutated")
		}
	})
}
