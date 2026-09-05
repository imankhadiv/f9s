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
