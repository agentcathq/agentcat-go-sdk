package sensitivekeys

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type vectors struct {
	Mask string `json:"mask"`
	Keys []struct {
		Key       string `json:"key"`
		Sensitive bool   `json:"sensitive"`
	} `json:"keys"`
	Trees []struct {
		Name     string `json:"name"`
		Input    any    `json:"input"`
		Expected any    `json:"expected"`
	} `json:"trees"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	data, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVectors(t *testing.T) {
	v := loadVectors(t)
	if v.Mask != Mask {
		t.Fatalf("mask = %q, vectors say %q", Mask, v.Mask)
	}
	for _, c := range v.Keys {
		if got := isSensitiveKey(c.Key); got != c.Sensitive {
			t.Errorf("isSensitiveKey(%q) = %v, want %v", c.Key, got, c.Sensitive)
		}
	}
	for _, c := range v.Trees {
		before, _ := json.Marshal(c.Input)
		got := MaskSensitiveKeys(c.Input)
		// reflect.DeepEqual compares maps without regard to key order.
		if !reflect.DeepEqual(got, c.Expected) {
			t.Errorf("%s:\n got  %#v\n want %#v", c.Name, got, c.Expected)
		}
		if after, _ := json.Marshal(c.Input); string(after) != string(before) {
			t.Errorf("%s: input changed", c.Name)
		}
	}
}

// A cycle or a tree deeper than the walk's limit becomes the mask, never an unchecked value.
func TestCycleAndDepth(t *testing.T) {
	cyclic := map[string]any{"a": 1}
	cyclic["self"] = cyclic
	got := MaskSensitiveKeys(cyclic).(map[string]any)
	if got["self"] != Mask || got["a"] != 1 {
		t.Errorf("cycle: got %#v", got)
	}

	deep := any("leaf")
	for range 200 {
		deep = map[string]any{"d": deep}
	}
	node := MaskSensitiveKeys(deep)
	for {
		m, ok := node.(map[string]any)
		if !ok {
			break
		}
		node = m["d"]
	}
	if node != Mask {
		t.Errorf("depth: the deepest value is %#v, want the mask", node)
	}
}
