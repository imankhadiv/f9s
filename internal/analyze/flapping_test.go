package analyze

import (
	"testing"
	"time"
)

func TestFlapping(t *testing.T) {
	t0 := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	ev := func(min int, res, vid string, from, to int) ScalingEvent {
		return ScalingEvent{At: t0.Add(time.Duration(min) * time.Minute), Resource: res,
			Changes: []ScalingChange{{VID: vid, From: from, To: to}}}
	}
	evs := []ScalingEvent{
		// flapping: 2→4→2→4 within 30 minutes
		ev(0, "ns/orders", "aaaaaaaaaaaaaaaa", 2, 4),
		ev(10, "ns/orders", "aaaaaaaaaaaaaaaa", 4, 2),
		ev(20, "ns/orders", "aaaaaaaaaaaaaaaa", 2, 4),
		// monotonic growth is not flapping
		ev(0, "ns/orders", "bbbbbbbbbbbbbbbb", 2, 4),
		ev(10, "ns/orders", "bbbbbbbbbbbbbbbb", 4, 8),
		ev(20, "ns/orders", "bbbbbbbbbbbbbbbb", 8, 12),
		// reversals spread over hours are ordinary daily load, not flapping
		ev(0, "ns/billing", "cccccccccccccccc", 2, 4),
		ev(200, "ns/billing", "cccccccccccccccc", 4, 2),
		ev(400, "ns/billing", "cccccccccccccccc", 2, 4),
	}
	flaps := Flapping(evs, time.Hour)
	if len(flaps) != 1 {
		t.Fatalf("flaps = %+v", flaps)
	}
	f := flaps[0]
	if f.Resource != "ns/orders" || f.Reversals != 2 || !f.At.Equal(t0.Add(20*time.Minute)) {
		t.Fatalf("flap = %+v", f)
	}
	if got := f.String(); got != "ns/orders vertex aaaaaaaa reversed direction 2× ([2 4 2 4])" {
		t.Fatalf("String = %q", got)
	}
}
