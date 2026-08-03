// Package inject implements AgentCat's pure, deterministic schema-injection
// pipeline: (config, listed tools) in → (advertised tools, registries) out.
package inject

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SchemaObject is a JSON object that preserves key order and the raw bytes
// of every value. Injection edits only the keys it owns; customer content
// round-trips byte-for-byte.
type SchemaObject struct {
	keys []string
	vals map[string]json.RawMessage
}

func NewSchemaObject() *SchemaObject {
	return &SchemaObject{vals: make(map[string]json.RawMessage)}
}

// ParseSchemaObject parses raw as a JSON object, preserving key order.
func ParseSchemaObject(raw []byte) (*SchemaObject, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("inject: schema is not a JSON object")
	}
	o := NewSchemaObject()
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := keyTok.(string)
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		o.Set(key, val)
	}
	if _, err := dec.Token(); err != nil { // consume closing '}'
		return nil, err
	}
	return o, nil
}

func (o *SchemaObject) Get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

func (o *SchemaObject) Has(key string) bool {
	_, ok := o.vals[key]
	return ok
}

// Set appends new keys at the end; existing keys keep their position.
func (o *SchemaObject) Set(key string, val json.RawMessage) {
	if _, exists := o.vals[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = val
}

func (o *SchemaObject) Delete(key string) {
	if _, exists := o.vals[key]; !exists {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *SchemaObject) Keys() []string { return append([]string(nil), o.keys...) }

// Clone returns an independent copy. RawMessage values are shared but never
// mutated in place by this package.
func (o *SchemaObject) Clone() *SchemaObject {
	c := NewSchemaObject()
	for _, k := range o.keys {
		c.Set(k, o.vals[k])
	}
	return c
}

func (o *SchemaObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(o.vals[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (o *SchemaObject) UnmarshalJSON(b []byte) error {
	parsed, err := ParseSchemaObject(b)
	if err != nil {
		return err
	}
	*o = *parsed
	return nil
}
