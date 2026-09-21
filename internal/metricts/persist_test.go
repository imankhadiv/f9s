package metricts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m", "ctx.json")
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	r := New(0)
	k := Key{ScopeJob, "j", CkptSize}
	r.Add(k, Point{Time: now.Add(-3 * time.Hour), Value: 1}) // too old to reload
	r.Add(k, Point{Time: now.Add(-time.Minute), Value: 2, Subtasks: []float64{1, 1}})
	r.Add(k, Point{Time: now, Value: 3})
	if err := r.Save(path); err != nil {
		t.Fatal(err)
	}

	back := New(0)
	back.Load(path, 2*time.Hour, now)
	got := back.Series(k)
	if len(got) != 2 || got[0].Value != 2 || got[1].Value != 3 {
		t.Fatalf("reloaded %+v", got)
	}
	if got[0].Subtasks != nil {
		t.Fatal("subtask distributions are not persisted")
	}
	if !got[1].Time.Equal(now) {
		t.Fatalf("time round-trip: %v", got[1].Time)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestLoadToleratesMissingOrCorruptFile(t *testing.T) {
	dir := t.TempDir()
	r := New(0)
	r.Load(filepath.Join(dir, "nope.json"), time.Hour, time.Now())
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte("{not json"), 0o644)
	r.Load(bad, time.Hour, time.Now())
	if len(r.Keys("")) != 0 {
		t.Fatal("bad input must leave the ring empty")
	}
}

func TestDataPathHonoursXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/xdg")
	if got := DataPath("prod"); got != "/xdg/f9s/metrics/prod.json" {
		t.Fatalf("DataPath = %s", got)
	}
}
