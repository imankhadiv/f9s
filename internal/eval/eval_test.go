package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/ai"
	"github.com/imankhadiv/f9s/internal/tools"
)

// scriptedAgent answers from the evidence deterministically — the harness
// self-test needs no real model.
type scriptedAgent struct{ answer string }

func (s scriptedAgent) Investigate(_ context.Context, seed string, _ *tools.Registry, _ func(string) string, onStep ai.StepFn, _ ai.ProposeFn) (string, error) {
	onStep("→ scripted")
	if strings.Contains(seed, "backpressure") || strings.Contains(seed, "BOTTLENECK") {
		return s.answer, nil
	}
	return s.answer, nil
}

func TestHarnessScoring(t *testing.T) {
	scenarios, err := LoadScenarios("../../evals/scenarios")
	if err != nil {
		t.Skipf("scenarios not present: %v", err)
	}
	good := scriptedAgent{answer: "HYPOTHESIS: heavy Calc is the bottleneck — busy and backpressured; also data skew with a hot subtask from one key imbalance."}
	bad := scriptedAgent{answer: "HYPOTHESIS: the kafka broker ran out of memory."}
	for _, sc := range scenarios {
		r := Run(sc, good)
		if r.Err != "" || !r.Correct || r.Halluc {
			t.Fatalf("%s good agent misgraded: %+v", sc.Name, r)
		}
		r = Run(sc, bad)
		if r.Correct || !r.Halluc {
			t.Fatalf("%s bad agent misgraded: %+v", sc.Name, r)
		}
	}
}
