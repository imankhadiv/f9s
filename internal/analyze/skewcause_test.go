package analyze

import "testing"

func TestClassifySourceImbalance(t *testing.T) {
	d := ClassifySkew(true, []float64{500, 0}, nil)
	if d.Cause != CausePartitionImbalance {
		t.Fatalf("skewed source must classify as partition imbalance, got %q", d.Cause)
	}
}

func TestClassifyHotKey(t *testing.T) {
	d := ClassifySkew(false, []float64{900, 100, 100, 100}, []float64{800, 200, 200, 200})
	if d.Cause != CauseHotKey {
		t.Fatalf("uneven records on keyed vertex = hot key, got %q", d.Cause)
	}
}

func TestClassifyExpensiveKeys(t *testing.T) {
	d := ClassifySkew(false, []float64{100, 100, 100, 100}, []float64{900, 100, 100, 100})
	if d.Cause != CauseExpensiveKey {
		t.Fatalf("even records + uneven busy = expensive keys, got %q", d.Cause)
	}
}

func TestClassifyHealthy(t *testing.T) {
	d := ClassifySkew(false, []float64{100, 110, 95, 100}, []float64{200, 210, 190, 205})
	if d.Cause != CauseNone {
		t.Fatalf("balanced vertex must classify as none, got %q", d.Cause)
	}
}
