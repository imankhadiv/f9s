package analyze

import "testing"

func TestSkewScoreUniformIsZero(t *testing.T) {
	if got := SkewScore([]float64{100, 100, 100, 100}); got != 0 {
		t.Fatalf("uniform load must score 0, got %d", got)
	}
}

func TestSkewScoreHotSubtask(t *testing.T) {
	// one subtask does everything: MAD/mean = 1.5 for n=4 → capped at 100
	got := SkewScore([]float64{400, 0, 0, 0})
	if got != 100 {
		t.Fatalf("one-hot distribution must cap at 100, got %d", got)
	}
	moderate := SkewScore([]float64{200, 100, 100, 100})
	if moderate <= 0 || moderate >= 50 {
		t.Fatalf("moderate skew should land between 0 and 50, got %d", moderate)
	}
}

func TestSkewScoreDegenerate(t *testing.T) {
	if SkewScore(nil) != 0 || SkewScore([]float64{5}) != 0 || SkewScore([]float64{0, 0}) != 0 {
		t.Fatal("degenerate inputs must score 0")
	}
}

func TestMaxAvgRatio(t *testing.T) {
	got := MaxAvgRatio([]float64{300, 100, 100, 100})
	if got < 1.9 || got > 2.1 { // 300 / 150
		t.Fatalf("want ~2.0, got %f", got)
	}
}

func TestSkylineSortsAndBins(t *testing.T) {
	out := Skyline([]float64{1, 9, 5, 3}, 2)
	if len(out) != 2 || out[0] <= out[1] {
		t.Fatalf("skyline must be descending buckets, got %v", out)
	}
	if got := Skyline([]float64{2, 1}, 4); len(got) != 2 {
		t.Fatalf("fewer values than width keeps them all, got %v", got)
	}
}
