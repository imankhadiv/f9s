package tools

import (
	"context"
	"strings"
	"testing"
)

// The rescale tool must exist, be gated as mutating (so it can only ever be
// proposed, never auto-run through the AI loop), and take a vertex + parallelism.
func TestRescaleVertexRegistered(t *testing.T) {
	reg := New(nil, "prod")

	tl, ok := reg.Get("rescale_vertex")
	if !ok {
		t.Fatal("rescale_vertex not registered")
	}
	if !tl.Mutating {
		t.Error("rescale_vertex must be Mutating so the AI can only propose it")
	}
	// It must be gated out of the read-only view.
	for _, r := range reg.List(false) {
		if r.Name == "rescale_vertex" {
			t.Error("rescale_vertex leaked into the read-only tool list")
		}
	}
	// Required params: vertex and parallelism.
	need := map[string]bool{"vertex": false, "parallelism": false}
	for _, p := range tl.Params {
		if _, ok := need[p.Name]; ok {
			if !p.Required {
				t.Errorf("param %q should be required", p.Name)
			}
			need[p.Name] = true
		}
	}
	for name, seen := range need {
		if !seen {
			t.Errorf("rescale_vertex missing required param %q", name)
		}
	}
}

// Bad parallelism is rejected before any client call — so a nil client is safe
// here and the guard is what's under test.
func TestRescaleVertexRejectsBadParallelism(t *testing.T) {
	reg := New(nil, "prod")
	tl, _ := reg.Get("rescale_vertex")
	for _, bad := range []string{"", "0", "-2", "two", "1.5"} {
		_, err := tl.Run(context.Background(), map[string]string{"jid": "j", "vertex": "v", "parallelism": bad})
		if err == nil {
			t.Errorf("parallelism %q should be rejected", bad)
			continue
		}
		if !strings.Contains(err.Error(), "positive integer") {
			t.Errorf("parallelism %q: unexpected error %v", bad, err)
		}
	}
}
