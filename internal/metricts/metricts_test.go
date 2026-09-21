package metricts

import (
	"math"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func pts(vals ...float64) []Point {
	out := make([]Point, len(vals))
	for i, v := range vals {
		out[i] = Point{Time: t0.Add(time.Duration(i) * 10 * time.Second), Value: v}
	}
	return out
}

func TestRingBoundsEachSeries(t *testing.T) {
	r := New(3)
	k := Key{Scope: ScopeJob, ID: "j1", Metric: "m"}
	for _, p := range pts(1, 2, 3, 4, 5) {
		r.Add(k, p)
	}
	if got := r.Values(k); len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Fatalf("ring kept %v, want [3 4 5]", got)
	}
	if last, ok := r.Last(k); !ok || last.Value != 5 {
		t.Fatalf("Last = %v %v", last, ok)
	}
}

func TestRingIgnoresOutOfOrderPoints(t *testing.T) {
	r := New(0)
	k := Key{Scope: ScopeTM, ID: "tm", Metric: "m"}
	r.Add(k, Point{Time: t0.Add(time.Minute), Value: 2})
	r.Add(k, Point{Time: t0, Value: 1})
	if got := r.Values(k); len(got) != 1 || got[0] != 2 {
		t.Fatalf("out-of-order point must be dropped, got %v", got)
	}
}

func TestRingCopiesSubtasks(t *testing.T) {
	r := New(0)
	k := Key{Scope: ScopeVertex, ID: VertexID("j", "v"), Metric: "busy"}
	subs := []float64{1, 2}
	r.Add(k, Point{Time: t0, Value: 2, Subtasks: subs})
	subs[0] = 99
	if got := r.Series(k)[0].Subtasks[0]; got != 1 {
		t.Fatalf("ring aliased caller slice: %v", got)
	}
}

func TestKeysAndPrune(t *testing.T) {
	r := New(0)
	r.Add(Key{ScopeJob, "live", "a"}, Point{Time: t0})
	r.Add(Key{ScopeJob, "gone", "a"}, Point{Time: t0})
	r.Add(Key{ScopeTM, "tm1", "a"}, Point{Time: t0})
	if n := len(r.Keys(ScopeJob)); n != 2 {
		t.Fatalf("Keys(job) = %d, want 2", n)
	}
	r.Prune(func(k Key) bool { return k.Scope != ScopeJob || k.ID == "live" })
	keys := r.Keys("")
	if len(keys) != 2 || keys[0].ID != "live" || keys[1].ID != "tm1" {
		t.Fatalf("after prune keys = %v", keys)
	}
}

func TestNilRingIsSafe(t *testing.T) {
	var r *Ring
	r.Add(Key{}, Point{})
	r.Prune(func(Key) bool { return false })
	if r.Series(Key{}) != nil || r.Keys("") != nil {
		t.Fatal("nil ring must read empty")
	}
	if _, ok := r.Last(Key{}); ok {
		t.Fatal("nil ring has no last point")
	}
}

func TestRateSkipsCounterResets(t *testing.T) {
	got := Rate(pts(0, 100, 300, 5, 105))
	want := []float64{10, 20, 10}
	if len(got) != len(want) {
		t.Fatalf("Rate = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Rate = %v, want %v", got, want)
		}
	}
}

func TestDeltaSurvivesReset(t *testing.T) {
	if got := Delta(pts(10, 20, 5, 15)); got != 20 {
		t.Fatalf("Delta = %v, want 20", got)
	}
}

func TestSlope(t *testing.T) {
	if _, ok := Slope(pts(1, 2)); ok {
		t.Fatal("two points is not a trend")
	}
	s, ok := Slope(pts(0, 10, 20, 30))
	if !ok || math.Abs(s-1) > 1e-9 {
		t.Fatalf("Slope = %v %v, want 1/s", s, ok)
	}
}

func TestSince(t *testing.T) {
	p := pts(1, 2, 3, 4)
	if got := Since(p, t0.Add(15*time.Second)); len(got) != 2 || got[0].Value != 3 {
		t.Fatalf("Since = %v", got)
	}
}

func TestConcurrentAddAndRead(t *testing.T) {
	r := New(10)
	k := Key{ScopeJob, "j", "m"}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				r.Add(k, Point{Time: time.Now(), Value: float64(i)})
				_ = r.Values(k)
			}
		}()
	}
	wg.Wait()
	if n := len(r.Series(k)); n > 10 {
		t.Fatalf("ring overflowed: %d", n)
	}
}

func TestJobOf(t *testing.T) {
	cases := map[Key]string{
		{Scope: ScopeJob, ID: "j1"}:                   "j1",
		{Scope: ScopeVertex, ID: VertexID("j2", "v")}: "j2",
		{Scope: ScopeTM, ID: "tm-1"}:                  "",
	}
	for k, want := range cases {
		if got := JobOf(k); got != want {
			t.Errorf("JobOf(%v) = %q, want %q", k, got, want)
		}
	}
}
