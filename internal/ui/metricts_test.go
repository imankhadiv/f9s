package ui

import (
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/metricts"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/pins"
	"github.com/imankhadiv/f9s/internal/views"
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

type fakeMetrics struct{ calls []string }

func (f *fakeMetrics) MetricValues(path string, keys []string) ([]model.Metric, error) {
	f.calls = append(f.calls, path)
	return []model.Metric{{ID: keys[0], Value: "7"}}, nil
}

func TestSamplePinsRecordsLivePinsOnly(t *testing.T) {
	ring := metricts.New(0)
	f := &fakeMetrics{}
	ps := []pins.Pin{
		{Display: "jm", Path: "/jobmanager/metrics", Keys: []string{"numRunningJobs"}},
		{Display: "gone", Path: "/jobs/dead/metrics", Keys: []string{"x"}, JID: "dead"},
		{Display: "live", Path: "/jobs/alive/metrics", Keys: []string{"y"}, JID: "alive"},
	}
	samplePins(f, ring, ps, map[string]bool{"alive": true}, time.Now())
	if len(f.calls) != 2 {
		t.Fatalf("a pin on a finished job must not be polled: %v", f.calls)
	}
	p, ok := ring.Last(metricts.Key{Scope: metricts.ScopePin, ID: pins.SeriesID(ps[0]), Metric: "value"})
	if !ok || p.Value != 7 {
		t.Fatalf("pin not recorded: %+v %v", p, ok)
	}
}

func TestContextSwitchKeepsEachContextsHistory(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := &config.Config{Contexts: map[string]config.Context{
		"a": {URL: "http://localhost:1"}, "b": {URL: "http://localhost:2"}}}
	k := metricts.Key{Scope: metricts.ScopeJob, ID: "job-a", Metric: metricts.CkptSize}

	app := New(cfg, "a", 2, nil, nil)
	app.metrics.Add(k, metricts.Point{Time: time.Now(), Value: 1})
	app.Update(views.SwitchContextMsg{Name: "b"})
	if _, ok := app.metrics.Last(k); ok {
		t.Fatal("context b must not show context a's series")
	}
	// a late sample from context a must not prune or pollute b
	bk := metricts.Key{Scope: metricts.ScopeJob, ID: "job-b", Metric: metricts.CkptSize}
	app.metrics.Add(bk, metricts.Point{Time: time.Now(), Value: 2})
	app.Update(sentinelMsg{ctx: "a", liveJIDs: map[string]bool{}, authoritative: true})
	if _, ok := app.metrics.Last(bk); !ok {
		t.Fatal("a stale sample from the previous context pruned the new one")
	}

	back := New(cfg, "a", 2, nil, nil)
	if p, ok := back.metrics.Last(k); !ok || p.Value != 1 {
		t.Fatal("context a's history was not saved under a on switch")
	}
}
