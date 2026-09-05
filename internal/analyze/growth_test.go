package analyze

import (
	"strings"
	"testing"
	"time"
)

func pts(sizes ...int64) []GrowthPoint {
	base := time.Now().Add(-time.Duration(len(sizes)) * time.Hour)
	out := make([]GrowthPoint, len(sizes))
	for i, s := range sizes {
		out[i] = GrowthPoint{T: base.Add(time.Duration(i) * time.Hour), Size: s}
	}
	return out
}

func TestStateGrowth(t *testing.T) {
	// stable: jitter around 10KB
	if v := StateGrowth(pts(10000, 9800, 10200, 9900, 10100), 0); v.Trend != "stable" {
		t.Fatalf("jitter should be stable, got %s (%.0f B/day)", v.Trend, v.BytesPerDay)
	}
	// growing: +100MB per hour → strongly growing
	gb := int64(1) << 30
	g := StateGrowth([]GrowthPoint{
		{time.Now().Add(-4 * time.Hour), 1 * gb},
		{time.Now().Add(-3 * time.Hour), 2 * gb},
		{time.Now().Add(-2 * time.Hour), 3 * gb},
		{time.Now().Add(-1 * time.Hour), 4 * gb},
		{time.Now(), 5 * gb},
	}, 0)
	if g.Trend != "growing" {
		t.Fatalf("want growing, got %s", g.Trend)
	}
	// ~24 GiB/day
	if g.BytesPerDay < 20*float64(gb) || g.BytesPerDay > 28*float64(gb) {
		t.Fatalf("slope off: %.0f B/day", g.BytesPerDay)
	}
	// ETA to a 100GB volume from 5GB latest at 24GB/day ≈ 4 days
	e := StateGrowth([]GrowthPoint{
		{time.Now().Add(-4 * time.Hour), 1 * gb},
		{time.Now().Add(-3 * time.Hour), 2 * gb},
		{time.Now().Add(-2 * time.Hour), 3 * gb},
		{time.Now().Add(-1 * time.Hour), 4 * gb},
		{time.Now(), 5 * gb},
	}, 100*gb)
	if e.EtaDays < 3 || e.EtaDays > 5 {
		t.Fatalf("ETA off: %.1f days", e.EtaDays)
	}
	if !strings.Contains(e.Summary(), "disk full in") {
		t.Fatalf("summary missing ETA: %s", e.Summary())
	}
	// too few points → stable
	if v := StateGrowth(pts(1, 1<<40), 0); v.Trend != "stable" {
		t.Fatal("two points must not compute a trend")
	}
}
