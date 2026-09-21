package pins

import (
	"testing"
)

func TestPinsRoundTrip(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if got := Load("ctx"); len(got) != 0 {
		t.Fatalf("empty context should load no pins, got %d", len(got))
	}
	want := []Pin{{Display: "heap", Path: "/tm", Keys: []string{"Status.JVM.Memory.Heap.Used"}}}
	Save("ctx", want)
	got := Load("ctx")
	if len(got) != 1 || got[0].Display != "heap" || got[0].Keys[0] != "Status.JVM.Memory.Heap.Used" {
		t.Fatalf("round-trip lost data: %+v", got)
	}
}

func TestLoadOrDefault(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if got := LoadOrDefault("fresh"); len(got) != len(Defaults()) || len(got) == 0 {
		t.Fatalf("a fresh context gets the curated pins, got %d", len(got))
	}
	Save("cleared", []Pin{})
	if got := LoadOrDefault("cleared"); len(got) != 0 {
		t.Fatalf("an explicitly emptied pin set must stay empty, got %d", len(got))
	}
	seen := map[string]bool{}
	for _, p := range Defaults() {
		if seen[SeriesID(p)] || seen[p.Display] {
			t.Fatalf("duplicate default pin %q", p.Display)
		}
		seen[SeriesID(p)], seen[p.Display] = true, true
	}
}
