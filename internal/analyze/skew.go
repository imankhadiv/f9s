package analyze

import "sort"

// SkewScore is Flink's own FLIP-418 formula computed client-side (so it
// works on any Flink version, not just 1.20+):
//
//	skew% = min(mean_absolute_deviation / mean × 100, 100)
//
// over per-subtask values. 0 for fewer than two subtasks or a zero mean.
func SkewScore(values []float64) int {
	if len(values) < 2 {
		return 0
	}
	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))
	if mean == 0 {
		return 0
	}
	mad := 0.0
	for _, v := range values {
		d := v - mean
		if d < 0 {
			d = -d
		}
		mad += d
	}
	mad /= float64(len(values))
	score := mad / mean * 100
	if score > 100 {
		score = 100
	}
	return int(score)
}

// MaxAvgRatio says how many times its fair share the hottest subtask does.
func MaxAvgRatio(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	mean, maxV := 0.0, 0.0
	for _, v := range values {
		mean += v
		if v > maxV {
			maxV = v
		}
	}
	mean /= float64(len(values))
	if mean == 0 {
		return 0
	}
	return maxV / mean
}

// Skyline bins values, sorted descending, into width buckets (bucket = mean
// of its slice) — the sorted-bar shape reads identically at parallelism 4
// or 512: a cliff on the left means hot subtasks.
func Skyline(values []float64, width int) []float64 {
	if len(values) == 0 || width <= 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Sort(sort.Reverse(sort.Float64Slice(sorted)))
	if len(sorted) <= width {
		return sorted
	}
	out := make([]float64, width)
	per := float64(len(sorted)) / float64(width)
	for i := 0; i < width; i++ {
		lo, hi := int(float64(i)*per), int(float64(i+1)*per)
		if hi > len(sorted) {
			hi = len(sorted)
		}
		sum := 0.0
		for _, v := range sorted[lo:hi] {
			sum += v
		}
		out[i] = sum / float64(hi-lo)
	}
	return out
}
