package tools

import "testing"

func TestRegistryReadWriteGate(t *testing.T) {
	// New wires the full registry; a nil client is fine since we never call.
	reg := New(nil, "prod")

	readOnly := reg.List(false)
	all := reg.List(true)
	if len(readOnly) == 0 {
		t.Fatal("expected some read-only tools")
	}
	if len(all) <= len(readOnly) {
		t.Fatalf("mutating tools should expand the list: %d vs %d", len(all), len(readOnly))
	}
	for _, tl := range readOnly {
		if tl.Mutating {
			t.Errorf("List(false) leaked a mutating tool: %s", tl.Name)
		}
	}

	// At least one known mutating tool must exist and be gated out of the
	// read-only view.
	var sawMutating bool
	for _, tl := range all {
		if tl.Mutating {
			sawMutating = true
			if _, ok := reg.Get(tl.Name); !ok {
				t.Errorf("Get(%q) missing for a listed tool", tl.Name)
			}
		}
	}
	if !sawMutating {
		t.Fatal("registry defines no mutating tools — read/write gate is untested")
	}

	if _, ok := reg.Get("does_not_exist"); ok {
		t.Error("Get of unknown tool should be !ok")
	}
}
