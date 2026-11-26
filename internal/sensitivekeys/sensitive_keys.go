// Package sensitivekeys masks the values of sensitive keys (credential headers, tokens,
// secrets) in an event before it leaves the server. It is a port of the shared reference
// implementation that every AgentCat SDK ports; testdata/vectors.json holds its cases.
package sensitivekeys

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"go.agentcat.com/sdk/v2/internal/core"
	"go.agentcat.com/sdk/v2/internal/walk"
)

// Mask replaces the value of a sensitive key.
const Mask = "[redacted]"

// A key matches a name when its canonical form equals the name or ends with "-" + name.
var exact = []string{
	"accesskey", "cb-access-sign", "cf-access-client-id", "dpop", "fastly-key",
	"jenkins-crumb", "paypal-transmission-sig", "plaid-verification",
	"sec-websocket-protocol", "x-airtable-content-mac", "x-amzn-ava-user-context",
	"x-amzn-oidc-data", "x-firebase-appcheck", "x-forge-oauth-system",
	"x-forge-oauth-user", "x-honeycomb-team", "x-insert-key", "x-jfrog-art-api",
	"x-nf-sign", "x-registry-config", "x-requestdigest", "x-sfdc-session",
	"x-trello-webhook", "x-vault-mfa", "x-vercel-protection-bypass", "x-wp-nonce",
}

// A key matches when its canonical form contains the word.
var contains = []string{
	"authorization", "secret", "password", "passwd", "passphrase", "cookie", "signature",
	"credential", "jwt", "assertion", "bearer", "csrf", "xsrf", "hmac", "saml",
	"api-key", "apikey", "access-key", "private-key", "privatekey", "subscription-key",
	"encryption-key", "signing-key", "shared-key", "sharedkey", "master-key",
	"account-key", "app-key", "appkey", "application-key", "functions-key", "sas-key",
	"license-key", "consumer-key", "client-key", "customer-key",
}

// A key matches when one hyphen-separated part of its canonical form equals the word.
var segment = []string{"auth", "token", "otp"}

// A key matches when its canonical form ends with the word.
var suffix = []string{"token"}

// The pair rule: a map that names a key in one entry and holds its value in another.
var pairNameKeys = []string{"name", "key", "header"}
var pairValueKeys = []string{"value", "values"}

// The children of a map stored under this exact key are JSON Schema property names.
const schemaPropertiesKey = "properties"

func camelSplit(text string) string {
	var out []byte
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if i > 0 && 'A' <= ch && ch <= 'Z' {
			prev := text[i-1]
			var nxt byte
			if i+1 < len(text) {
				nxt = text[i+1]
			}
			if ('a' <= prev && prev <= 'z') || ('0' <= prev && prev <= '9') {
				out = append(out, '-')
			} else if ('A' <= prev && prev <= 'Z') && ('a' <= nxt && nxt <= 'z') {
				out = append(out, '-')
			}
		}
		out = append(out, ch)
	}
	return string(out)
}

func canonicalParts(text string) []string {
	out := make([]byte, 0, len(text))
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if 'A' <= ch && ch <= 'Z' {
			out = append(out, ch+32)
		} else if ('a' <= ch && ch <= 'z') || ('0' <= ch && ch <= '9') {
			out = append(out, ch)
		} else {
			out = append(out, '-')
		}
	}
	parts := strings.FieldsFunc(string(out), func(ch rune) bool { return ch == '-' })
	for len(parts) > 0 && strings.Trim(parts[len(parts)-1], "0123456789") == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func partsMatch(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	joined := strings.Join(parts, "-")
	for _, name := range exact {
		if joined == name || strings.HasSuffix(joined, "-"+name) {
			return true
		}
	}
	for _, word := range contains {
		if strings.Contains(joined, word) {
			return true
		}
	}
	for _, part := range parts {
		if slices.Contains(segment, part) {
			return true
		}
	}
	for _, word := range suffix {
		if strings.HasSuffix(joined, word) {
			return true
		}
	}
	return false
}

func isSensitiveKey(key string) bool {
	return partsMatch(canonicalParts(key)) || partsMatch(canonicalParts(camelSplit(key)))
}

func canonical(key string) string {
	return strings.Join(canonicalParts(key), "-")
}

func maskValue(value any, underProperties bool, seen map[uintptr]bool, depth int) any {
	value = jsonValue(value)
	if _, isBool := value.(bool); value == nil || isBool {
		return value
	}
	if _, isMap := value.(map[string]any); isMap && underProperties {
		return maskSensitiveKeys(value, false, seen, depth)
	}
	return Mask
}

// MaskSensitiveKeys returns a masked copy of value. It never changes value.
func MaskSensitiveKeys(value any) any {
	return maskSensitiveKeys(value, false, map[uintptr]bool{}, walk.MaxDepth)
}

// maskSensitiveKeys masks a copy of value. underProperties is true when value is the map
// stored under the key "properties". Like walk.Bounded, a cycle or a tree deeper than
// walk.MaxDepth stops the walk; here the stopped subtree becomes the mask.
func maskSensitiveKeys(value any, underProperties bool, seen map[uintptr]bool, depth int) any {
	value = jsonValue(value)
	if list, ok := value.([]any); ok {
		if len(list) == 0 {
			return list
		}
		ptr := reflect.ValueOf(list).Pointer()
		if seen[ptr] || depth <= 0 {
			return Mask
		}
		seen[ptr] = true
		defer delete(seen, ptr)
		out := make([]any, len(list))
		for i, item := range list {
			out[i] = maskSensitiveKeys(item, false, seen, depth-1)
		}
		return out
	}
	m, ok := value.(map[string]any)
	if !ok {
		return value
	}
	ptr := reflect.ValueOf(m).Pointer()
	if seen[ptr] || depth <= 0 {
		return Mask
	}
	seen[ptr] = true
	defer delete(seen, ptr)
	out := make(map[string]any, len(m))
	for key, child := range m {
		if isSensitiveKey(key) {
			out[key] = maskValue(child, underProperties, seen, depth-1)
		} else {
			out[key] = maskSensitiveKeys(child, key == schemaPropertiesKey, seen, depth-1)
		}
	}
	namesASensitiveKey := false
	for key, child := range m {
		if slices.Contains(pairNameKeys, canonical(key)) {
			if name, isString := jsonValue(child).(string); isString && isSensitiveKey(name) {
				namesASensitiveKey = true
			}
		}
	}
	if namesASensitiveKey {
		for key, child := range m {
			if slices.Contains(pairValueKeys, canonical(key)) {
				out[key] = maskValue(child, false, seen, depth-1)
			}
		}
	}
	return out
}

// jsonValue turns a typed value (http.Header, a struct) into the JSON tree that the SDK
// sends, with the JSON round trip that truncation uses. A value that cannot become JSON
// becomes the mask.
func jsonValue(value any) any {
	switch value.(type) {
	case nil, bool, string, float32, float64, json.Number, map[string]any, []any,
		int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return value
	}
	if converted, ok := core.JSONRoundTrip(value); ok {
		return converted
	}
	return Mask
}
