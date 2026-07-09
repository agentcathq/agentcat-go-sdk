// Package validation validates customer-supplied event metadata (tags)
// against AgentCat's client-side constraints before events are published.
package validation

import (
	"regexp"
	"sort"
	"strings"

	"go.agentcat.com/sdk/internal/logging"
)

const (
	// MaxTagKeyLength is the maximum allowed length for a tag key.
	MaxTagKeyLength = 32

	// MaxTagValueLength is the maximum allowed length for a tag value.
	MaxTagValueLength = 200

	// MaxTagEntries is the maximum number of tag entries per event.
	MaxTagEntries = 50
)

var tagKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9$_.:\- ]+$`)

// ValidateTags validates and filters a tags map against AgentCat tag
// constraints. Invalid entries are logged as warnings and dropped.
// Returns nil if no valid entries remain (or the input is empty/nil).
//
// Constraints (mirrors the TypeScript SDK's validateTags):
//   - keys must match ^[a-zA-Z0-9$_.:\- ]+$ and be at most 32 characters
//   - values must be at most 200 characters and contain no newlines
//   - at most 50 entries per event (extras dropped deterministically by key order)
func ValidateTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	logger := logging.New()

	// Sort keys so per-entry validation and the max-entries cutoff are
	// deterministic (Go map iteration order is randomized).
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	valid := make(map[string]string, len(tags))
	validKeys := make([]string, 0, len(tags))

	for _, key := range keys {
		value := tags[key]

		if !tagKeyRegex.MatchString(key) {
			logger.Warnf("Dropping invalid tag: %q - key contains invalid characters or is empty", key)
			continue
		}

		if len(key) > MaxTagKeyLength {
			logger.Warnf("Dropping invalid tag: %q - key exceeds max length of %d", key, MaxTagKeyLength)
			continue
		}

		if len(value) > MaxTagValueLength {
			logger.Warnf("Dropping invalid tag: %q - value exceeds max length of %d", key, MaxTagValueLength)
			continue
		}

		if strings.Contains(value, "\n") {
			logger.Warnf("Dropping invalid tag: %q - value contains newline character", key)
			continue
		}

		valid[key] = value
		validKeys = append(validKeys, key)
	}

	if len(valid) == 0 {
		return nil
	}

	if len(validKeys) > MaxTagEntries {
		dropped := len(validKeys) - MaxTagEntries
		logger.Warnf("Dropping %d tag(s) - exceeds maximum of %d entries per event", dropped, MaxTagEntries)
		for _, key := range validKeys[MaxTagEntries:] {
			delete(valid, key)
		}
	}

	return valid
}
