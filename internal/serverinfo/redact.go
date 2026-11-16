package serverinfo

import "fmt"

// identityKeys name the values at the top level of a list item that are never
// redacted: they identify the item, as the tool name does on an event.
var identityKeys = map[string]struct{}{
	"name":        {},
	"uri":         {},
	"uriTemplate": {},
}

// redactText runs the customer's redaction function on one string. A panic
// in the function is returned as an error: the heartbeat then sends nothing,
// so no text leaves that the function did not process.
func redactText(redact func(string) string, s string) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("redaction function panicked: %v", r)
		}
	}()
	return redact(s), nil
}

// redactFeatures runs redact on every string value of every list item, in
// place. Keys never change, and the identity values at the top level of an
// item are skipped.
func redactFeatures(features map[string][]any, redact func(string) string) error {
	for _, items := range features {
		for i, item := range items {
			obj, ok := item.(map[string]any)
			if !ok {
				redacted, err := redactValue(item, redact)
				if err != nil {
					return err
				}
				items[i] = redacted
				continue
			}
			for key, value := range obj {
				if _, identity := identityKeys[key]; identity {
					continue
				}
				redacted, err := redactValue(value, redact)
				if err != nil {
					return err
				}
				obj[key] = redacted
			}
		}
	}
	return nil
}

// redactValue returns value with redact applied to each string it holds.
// Maps and slices are changed in place.
func redactValue(value any, redact func(string) string) (any, error) {
	switch v := value.(type) {
	case string:
		return redactText(redact, v)
	case map[string]any:
		for key, inner := range v {
			redacted, err := redactValue(inner, redact)
			if err != nil {
				return nil, err
			}
			v[key] = redacted
		}
		return v, nil
	case []any:
		for i, inner := range v {
			redacted, err := redactValue(inner, redact)
			if err != nil {
				return nil, err
			}
			v[i] = redacted
		}
		return v, nil
	default:
		return value, nil
	}
}
