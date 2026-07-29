package core

import (
	"maps"
)

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
