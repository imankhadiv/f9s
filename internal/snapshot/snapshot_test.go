package snapshot

import (
	"testing"
	"time"
)

func TestDiffAndJournal(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	a := Snap{Time: time.Now(), JID: "j1", Name: "job",
		Parallelism: map[string]int{"src": 1}, CkptInterval: 30000, Backend: "hashmap", Restart: "default"}
	changed, diffs := AppendIfChanged("test", a)
	if !changed || diffs[0] != "first observed" {
		t.Fatalf("first sighting: %v %v", changed, diffs)
	}
	if changed, _ := AppendIfChanged("test", a); changed {
		t.Fatal("identical snapshot must not append")
	}
	b := a
	b.Parallelism = map[string]int{"src": 2}
	b.CkptInterval = 10000
	changed, diffs = AppendIfChanged("test", b)
	if !changed || len(diffs) != 2 {
		t.Fatalf("want 2 diffs, got %v %v", changed, diffs)
	}
	if got := len(Load("test", "j1")); got != 2 {
		t.Fatalf("journal length %d", got)
	}
}
