package inject

import (
	"encoding/json"
	"testing"
)

func TestSchemaObjectRoundTripPreservesOrderAndBytes(t *testing.T) {
	in := []byte(`{"zeta":{"type":"string"},"alpha":1.50,"mid":[true,null]}`)
	o, err := ParseSchemaObject(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"zeta":{"type":"string"},"alpha":1.50,"mid":[true,null]}` {
		t.Errorf("round trip changed bytes/order: %s", out)
	}
}

func TestSchemaObjectSetAppendsAndDeleteRemoves(t *testing.T) {
	o, _ := ParseSchemaObject([]byte(`{"b":1,"a":2}`))
	o.Set("c", json.RawMessage(`3`))
	o.Set("b", json.RawMessage(`9`)) // existing key keeps position
	o.Delete("a")
	out, _ := json.Marshal(o)
	if string(out) != `{"b":9,"c":3}` {
		t.Errorf("got %s", out)
	}
}

func TestParseSchemaObjectRejectsNonObjects(t *testing.T) {
	for _, bad := range []string{`[]`, `"s"`, `3`, `null`, `{"a":`} {
		if _, err := ParseSchemaObject([]byte(bad)); err == nil {
			t.Errorf("expected error for %s", bad)
		}
	}
}

func TestSchemaObjectCloneIsIndependent(t *testing.T) {
	o, _ := ParseSchemaObject([]byte(`{"a":1}`))
	c := o.Clone()
	c.Set("b", json.RawMessage(`2`))
	c.Delete("a")
	if out, _ := json.Marshal(o); string(out) != `{"a":1}` {
		t.Errorf("clone mutated original: %s", out)
	}
}
