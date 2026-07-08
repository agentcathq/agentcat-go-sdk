package core

import (
	"encoding/json"
	"maps"
)

// IdentitiesEqual reports whether two user identities are deeply equal.
// UserData values are compared by their JSON encoding, mirroring the
// TypeScript SDK's areIdentitiesEqual.
func IdentitiesEqual(a, b *UserIdentity) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.UserID != b.UserID {
		return false
	}
	if a.UserName != b.UserName {
		return false
	}

	if len(a.UserData) != len(b.UserData) {
		return false
	}
	for key, aVal := range a.UserData {
		bVal, ok := b.UserData[key]
		if !ok {
			return false
		}
		aJSON, aErr := json.Marshal(aVal)
		bJSON, bErr := json.Marshal(bVal)
		if aErr != nil || bErr != nil {
			// Unserializable values: fall back to inequality so a change is
			// assumed and the identify event is published (fail open).
			return false
		}
		if string(aJSON) != string(bJSON) {
			return false
		}
	}

	return true
}

// MergeIdentities merges a new identity into a previous one: UserID and
// UserName are overwritten, UserData fields are deep-merged (next wins on
// conflicts). The inputs are not mutated.
func MergeIdentities(previous, next *UserIdentity) *UserIdentity {
	if next == nil {
		return previous
	}
	if previous == nil {
		return next
	}

	merged := &UserIdentity{
		UserID:   next.UserID,
		UserName: next.UserName,
	}

	if len(previous.UserData) > 0 || len(next.UserData) > 0 {
		merged.UserData = make(map[string]any, len(previous.UserData)+len(next.UserData))
		maps.Copy(merged.UserData, previous.UserData)
		maps.Copy(merged.UserData, next.UserData)
	}

	return merged
}
