package core

import "testing"

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
