package analyze

import (
	"strings"
	"testing"
)

func TestAdviseParallelismWithSlotMath(t *testing.T) {
	vs := []VertexSample{
		{Name: "source", Level: "high", BusyMsPerSec: 300, Parallelism: 2},
		{Name: "window", Level: "ok", BusyMsPerSec: 960, Parallelism: 2},
		{Name: "sink", Level: "ok", BusyMsPerSec: 100, Parallelism: 2},
	}
	advice := Advise(vs, 3)
	if len(advice) == 0 {
		t.Fatal("saturated bottleneck must produce advice")
	}
	text := advice[0].Text
	for _, want := range []string{"window", "2 → 4", "+2 slots", "3 free"} {
		if !strings.Contains(text, want) {
			t.Fatalf("advice missing %q: %s", want, text)
		}
	}
}

func TestAdviseInsufficientSlots(t *testing.T) {
	vs := []VertexSample{
		{Name: "source", Level: "high", BusyMsPerSec: 300, Parallelism: 8},
		{Name: "agg", Level: "ok", BusyMsPerSec: 950, Parallelism: 8},
	}
	advice := Advise(vs, 2)
	if !strings.Contains(advice[0].Text, "add TaskManagers") {
		t.Fatalf("must warn about slots: %s", advice[0].Text)
	}
}

func TestAdviseSlowExternalSink(t *testing.T) {
	vs := []VertexSample{
		{Name: "source", Level: "high", BusyMsPerSec: 400, Parallelism: 4},
		{Name: "sink", Level: "high", BusyMsPerSec: 120, Parallelism: 4},
	}
	advice := Advise(vs, 10)
	found := false
	for _, a := range advice {
		if strings.Contains(a.Text, "external sink") {
			found = true
		}
	}
	if !found {
		t.Fatalf("lazy backpressured sink must be flagged: %+v", advice)
	}
}

func TestAdviseHealthyJobSilent(t *testing.T) {
	vs := []VertexSample{
		{Name: "source", Level: "ok", BusyMsPerSec: 200, Parallelism: 2},
		{Name: "sink", Level: "ok", BusyMsPerSec: 100, Parallelism: 2},
	}
	if advice := Advise(vs, 5); advice != nil {
		t.Fatalf("no backpressure → no advice, got %+v", advice)
	}
}

func TestCheckpointAdviceRisingDurations(t *testing.T) {
	// newest-first: recent avg 12s vs prior avg 2s → rising
	got := CheckpointAdvice([]int64{12000, 12000, 12000, 2000, 2000, 2000}, 60000, 600000, false)
	if len(got) == 0 || got[0].Severity != "warn" {
		t.Fatalf("want rising-duration warn, got %+v", got)
	}
}

func TestCheckpointAdviceUnalignedHint(t *testing.T) {
	got := CheckpointAdvice([]int64{40000}, 60000, 600000, false)
	if len(got) != 1 {
		t.Fatalf("want unaligned hint, got %+v", got)
	}
	if got := CheckpointAdvice([]int64{40000}, 60000, 600000, true); len(got) != 0 {
		t.Fatalf("already unaligned — no hint expected, got %+v", got)
	}
}

func TestCheckpointAdviceNearTimeout(t *testing.T) {
	got := CheckpointAdvice([]int64{9000}, 60000, 10000, false)
	if len(got) != 1 || got[0].Severity != "warn" {
		t.Fatalf("want near-timeout warn, got %+v", got)
	}
	if got := CheckpointAdvice([]int64{100}, 30000, 600000, false); len(got) != 0 {
		t.Fatalf("healthy checkpoints — no advice expected, got %+v", got)
	}
}
