package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDeploymentModeString(t *testing.T) {
	cases := map[DeploymentMode]string{
		ModeApplication:    "application",
		ModeSession:        "session",
		ModeStandalone:     "standalone",
		ModeUnknown:        "unknown",
		DeploymentMode(99): "unknown",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Errorf("DeploymentMode(%d).String() = %q, want %q", int(m), got, want)
		}
	}
}

func TestBackpressureLevelSpellings(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"backpressure-level":"high"}`, "high"},
		{`{"backpressureLevel":"low"}`, "low"},
		{`{"backpressure-level":"ok","backpressureLevel":"high"}`, "high"}, // camel wins
		{`{"status":"deprecated"}`, ""},
	}
	for _, c := range cases {
		var b Backpressure
		if err := json.Unmarshal([]byte(c.in), &b); err != nil {
			t.Fatal(err)
		}
		if got := b.Level(); got != c.want {
			t.Errorf("%s: Level() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBackpressureMaxRatioPercent(t *testing.T) {
	var b Backpressure
	if got := b.MaxRatioPercent(); got != -1 {
		t.Errorf("no subtasks: got %d, want -1", got)
	}
	if err := json.Unmarshal([]byte(`{"subtasks":[{"ratio":0.1},{"ratio":0.75},{"ratio":0.5}]}`), &b); err != nil {
		t.Fatal(err)
	}
	if got := b.MaxRatioPercent(); got != 75 {
		t.Errorf("got %d, want 75", got)
	}
}

func TestSavepointStatusFailed(t *testing.T) {
	var ok, noLoc, cause SavepointStatus
	ok.Status.ID, ok.Operation.Location = "COMPLETED", "file:///sp/1"
	noLoc.Status.ID = "COMPLETED"
	cause.Operation.FailureCause.Class = "java.io.IOException"
	var inProgress SavepointStatus
	inProgress.Status.ID = "IN_PROGRESS"

	if ok.Failed() {
		t.Error("completed with a location must not be failed")
	}
	if !noLoc.Failed() {
		t.Error("completed without a location must be failed")
	}
	if !cause.Failed() {
		t.Error("a failure cause must be failed")
	}
	if inProgress.Failed() {
		t.Error("in-progress without a cause must not be failed")
	}
}

func TestFmtAge(t *testing.T) {
	for _, ts := range []int64{0, -5} {
		if got := FmtAge(ts); got != "-" {
			t.Errorf("FmtAge(%d) = %q, want -", ts, got)
		}
	}
	got := FmtAge(time.Now().Add(-2 * time.Hour).UnixMilli())
	if got != "2h00m" && got != "2h01m" {
		t.Errorf("FmtAge(2h ago) = %q", got)
	}
}

func TestJVMHelpers(t *testing.T) {
	s := NewJVMStats()
	if s.HeapUsed != -1 || s.NetTotal != -1 || s.CPULoad != -1 || s.GCCount != -1 {
		t.Errorf("NewJVMStats must start fully unknown: %+v", s)
	}
	cases := []struct{ used, total, want float64 }{
		{50, 100, 0.5}, {-1, 100, -1}, {10, 0, -1}, {10, -1, -1}, {0, 100, 0},
	}
	for _, c := range cases {
		if got := Frac(c.used, c.total); got != c.want {
			t.Errorf("Frac(%v,%v) = %v, want %v", c.used, c.total, got, c.want)
		}
	}
	if got := (JMMetrics{HeapUsed: 256, HeapMax: 1024}).HeapPct(); got != 25 {
		t.Errorf("HeapPct = %d, want 25", got)
	}
	if got := (JMMetrics{HeapUsed: 256}).HeapPct(); got != -1 {
		t.Errorf("HeapPct with unknown max = %d, want -1", got)
	}
}
