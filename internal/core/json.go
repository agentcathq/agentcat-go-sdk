package core

import "encoding/json"

// JSONRoundTrip converts any value (including structs and slices of structs)
// to generic JSON-shaped values (map[string]any, []any, primitives) by
// marshaling to JSON and unmarshaling back. ok is false when v is not
// JSON-serializable.
func JSONRoundTrip(v any) (result any, ok bool) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, false
	}
	return result, true
}
