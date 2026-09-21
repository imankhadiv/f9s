package analyze

import "testing"

func TestParseScalingReport(t *testing.T) {
	msg := "Scaling execution enabled, begin scaling vertices:" +
		"{ Vertex ID 6D2677A0ECC3FD8DF0B72EC675EDF8F4 | Parallelism 1 -> 2 | Processing capacity 800.00 -> 1600.00 | Target data rate 1000.00}" +
		"{ Vertex ID bc764cd8ddf7a0cff126f51c16239658 | Parallelism 4 -> 2 | Processing capacity 9000.00 -> 4500.00 | Target data rate 1000.00}"
	got := ParseScalingReport(msg)
	if len(got) != 2 || got[0].VID != "6d2677a0ecc3fd8df0b72ec675edf8f4" || got[0].From != 1 || got[0].To != 2 || !got[0].Applied {
		t.Fatalf("parsed %+v", got)
	}
	if got[1].From != 4 || got[1].To != 2 {
		t.Fatalf("second vertex %+v", got[1])
	}
	rec := ParseScalingReport("Recommended parallelism change: { Vertex ID bc764cd8ddf7a0cff126f51c16239658 | Parallelism 2 -> 3 | … }")
	if len(rec) != 1 || rec[0].Applied {
		t.Fatalf("recommendation-only report = %+v", rec)
	}
	if ParseScalingReport("Job status changed") != nil {
		t.Fatal("non-scaling message must yield nothing")
	}
}

func TestEstimateParallelism(t *testing.T) {
	cases := []struct {
		name   string
		cur    int
		busy   float64
		maxPar int
		want   int
	}{
		{"inside dead band", 4, 0.8, 0, 4},
		{"saturated doubles-ish", 4, 1.0, 0, 6},
		{"idle scales down", 8, 0.1, 0, 2},
		{"never below 1", 2, 0.01, 0, 1},
		{"capped at max parallelism", 10, 1.0, 12, 12},
		{"unknown busy keeps current", 3, -1, 0, 3},
	}
	for _, c := range cases {
		if got := EstimateParallelism(c.cur, c.busy, DefaultTargetUtilization, DefaultUtilizationBound, c.maxPar); got != c.want {
			t.Errorf("%s: EstimateParallelism = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestConfigFloat(t *testing.T) {
	conf := map[string]string{"kubernetes.operator.job.autoscaler.target.utilization": "0.6", "job.autoscaler.target.utilization.boundary": "x"}
	if got := ConfigFloat(conf, "target.utilization", DefaultTargetUtilization); got != 0.6 {
		t.Fatalf("legacy prefix = %v", got)
	}
	if got := ConfigFloat(conf, "target.utilization.boundary", DefaultUtilizationBound); got != DefaultUtilizationBound {
		t.Fatalf("invalid value must fall back, got %v", got)
	}
}
