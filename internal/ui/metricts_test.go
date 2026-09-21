package ui

import (
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/metricts"
	"github.com/imankhadiv/f9s/internal/model"
)

func TestRecordCheckpointAddsNewestCompletedOnce(t *testing.T) {
	ring := metricts.New(0)
	cp := model.Checkpoints{History: []model.Checkpoint{
		{ID: 4, Status: "IN_PROGRESS", TriggerTimestamp: 4000},
		{ID: 3, Status: "COMPLETED", IsSavepoint: true, TriggerTimestamp: 3000, StateSize: 999},
		{ID: 2, Status: "COMPLETED", TriggerTimestamp: 2000, StateSize: 200, EndToEndDuration: 50},
		{ID: 1, Status: "COMPLETED", TriggerTimestamp: 1000, StateSize: 100},
	}}
	recordCheckpoint(ring, "j", cp)
	recordCheckpoint(ring, "j", cp) // same checkpoint re-read: no duplicate

	size := ring.Series(metricts.Key{Scope: metricts.ScopeJob, ID: "j", Metric: metricts.CkptSize})
	if len(size) != 1 || size[0].Value != 200 {
		t.Fatalf("size series = %+v, want one point of 200 (savepoints skipped)", size)
	}
	dur, ok := ring.Last(metricts.Key{Scope: metricts.ScopeJob, ID: "j", Metric: metricts.CkptDuration})
	if !ok || dur.Value != 50 {
		t.Fatalf("duration = %+v %v", dur, ok)
	}
}

func TestAppPersistsMetricRingAcrossRestarts(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := &config.Config{Contexts: map[string]config.Context{"dev": {URL: "http://localhost:1"}}}
	a := New(cfg, "dev", 2, nil, nil)
	k := metricts.Key{Scope: metricts.ScopeJob, ID: "j", Metric: metricts.CkptSize}
	a.metrics.Add(k, metricts.Point{Time: time.Now(), Value: 42})
	a.SaveState()

	b := New(cfg, "dev", 2, nil, nil)
	if p, ok := b.metrics.Last(k); !ok || p.Value != 42 {
		t.Fatalf("history not restored: %+v %v", p, ok)
	}
}
