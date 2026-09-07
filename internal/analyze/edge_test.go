package analyze

import "testing"

func TestBackpressureEdge(t *testing.T) {
	cases := []struct {
		name          string
		busy, in, out []float64
		wantKind      string
	}{
		{"downstream slow (output full)", []float64{950, 900}, []float64{0.1, 0.2}, []float64{0.98, 0.95}, "downstream"},
		{"upstream starved (input full, not busy)", []float64{80, 60}, []float64{0.97, 0.9}, []float64{0.05, 0.1}, "upstream"},
		{"cpu-bound (busy, clear pools)", []float64{950, 880}, []float64{0.2, 0.1}, []float64{0.1, 0.2}, "cpu"},
		{"idle", []float64{5, 10}, []float64{0.1, 0.1}, []float64{0.1, 0.1}, "idle"},
		{"flowing", []float64{400, 350}, []float64{0.3, 0.2}, []float64{0.3, 0.4}, "ok"},
		{"mismatched lengths", []float64{400}, []float64{}, []float64{}, "ok"},
	}
	for _, tc := range cases {
		v := BackpressureEdge(tc.busy, tc.in, tc.out)
		if v.Kind != tc.wantKind {
			t.Errorf("%s: got %q (%s), want %q", tc.name, v.Kind, v.Summary, tc.wantKind)
		}
	}
}

// Output saturation must win even when a subtask is also busy — a busy victim
// of downstream backpressure is still a downstream problem.
func TestBackpressureEdgeOutputWinsOverBusy(t *testing.T) {
	v := BackpressureEdge([]float64{950}, []float64{0.1}, []float64{0.99})
	if v.Kind != "downstream" {
		t.Errorf("output-full+busy should read as downstream, got %q", v.Kind)
	}
	if len(v.HotSubtasks) != 1 || v.HotSubtasks[0] != 0 {
		t.Errorf("hot subtask should be flagged, got %v", v.HotSubtasks)
	}
}
