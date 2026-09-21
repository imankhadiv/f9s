package fleet

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/metricts"
	"github.com/imankhadiv/f9s/internal/model"
)

type fakeSource struct {
	tmErr bool
	gc    float64 // cumulative GC ms handed out, advanced per call
	step  float64
}

func (f *fakeSource) TaskManagers() ([]model.TaskManager, error) {
	if f.tmErr {
		return nil, errors.New("down")
	}
	return []model.TaskManager{{ID: "tm-b"}, {ID: "tm-a"}}, nil
}

func (f *fakeSource) JVMStats(path string) (model.JVMStats, error) {
	st := model.NewJVMStats()
	st.HeapUsed, st.HeapMax = 950, 1000
	st.MetaUsed, st.MetaMax = 10, 100
	if strings.HasPrefix(path, "/taskmanagers/") {
		f.gc += f.step
		st.GCTimeMs = f.gc
		st.NetAvail, st.NetTotal = 0, 2048
		st.ManagedUsed, st.ManagedTotal = 50, 100
	}
	return st, nil
}

func TestCollectAndRows(t *testing.T) {
	ring := metricts.New(0)
	src := &fakeSource{step: 7500} // per TM per 15s sample → 250 ms/s each
	t0 := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	var live map[string]bool
	for i := 0; i < 5; i++ {
		live = Collect(src, ring, t0.Add(time.Duration(i)*15*time.Second))
	}
	if !live["tm-a"] || !live["tm-b"] {
		t.Fatalf("live = %v", live)
	}
	rows := Rows(ring, t0.Add(time.Minute))
	if len(rows) != 3 || rows[0].ID != JMID || rows[1].ID != "tm-a" || rows[2].ID != "tm-b" {
		t.Fatalf("row order = %+v", rows)
	}
	tm := rows[1]
	// both TMs share one fake counter, so each advances 2×7500 per sample
	if tm.GCMsPerSec < 900 || tm.GCMsPerSec > 1100 {
		t.Fatalf("GC ms/s = %v", tm.GCMsPerSec)
	}
	if tm.Heap != 0.95 || tm.Network != 1 || tm.Managed != 0.5 || len(tm.HeapTrend) != 5 {
		t.Fatalf("tm row = %+v", tm)
	}
	var texts []string
	for _, v := range tm.Verdicts {
		texts = append(texts, v.Level+":"+v.Text)
	}
	joined := strings.Join(texts, "|")
	for _, want := range []string{"crit:GC thrashing", "crit:heap 95% full", "network buffers exhausted"} {
		if !strings.Contains(joined, want) {
			t.Errorf("verdicts %q missing %q", joined, want)
		}
	}
	if rows[0].GCMsPerSec != -1 || rows[0].Network != -1 {
		t.Fatalf("JM without GC/network metrics must read unknown: %+v", rows[0])
	}
}

func TestCollectReportsNoLiveSetOnFailure(t *testing.T) {
	if live := Collect(&fakeSource{tmErr: true}, metricts.New(0), time.Now()); live != nil {
		t.Fatal("a failed TM list must not look like 'no TMs' (that would prune history)")
	}
}

func TestVerdictThresholds(t *testing.T) {
	if v := verdicts(Row{Heap: 0.5, GCMsPerSec: 50, Meta: 0.2, Network: 0.1}); len(v) != 0 {
		t.Fatalf("healthy JVM flagged: %v", v)
	}
	v := verdicts(Row{Heap: 0.92, GCMsPerSec: 10, Meta: 0.95, Network: -1})
	if len(v) != 2 || v[0].Level != "warn" || !strings.Contains(v[1].Text, "metaspace") {
		t.Fatalf("verdicts = %v", v)
	}
}
