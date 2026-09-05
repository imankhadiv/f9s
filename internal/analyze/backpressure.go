// Package analyze holds f9s's deterministic diagnosis helpers — the
// evidence-producing analyzers that views (and later, AI explain) build on.
package analyze

// VertexSample is one vertex's backpressure sample in topological order.
type VertexSample struct {
	ID           string
	Name         string
	Level        string  // ok / low / high / "" (unknown)
	BusyMsPerSec float64 // avg busyTimeMsPerSecond across subtasks; -1 unknown
	Parallelism  int
}

func backpressured(level string) bool { return level == "low" || level == "high" }

// Bottleneck returns the index of the likely bottleneck vertex, or -1 when
// nothing is backpressured. Flink's rule of thumb: backpressure propagates
// upstream, so the culprit is the first vertex downstream of the
// backpressured chain that is itself not backpressured — the busiest such
// vertex when busy metrics are available. If backpressure reaches the sink
// (no non-backpressured vertex downstream), the last backpressured vertex is
// the best suspect.
func Bottleneck(vs []VertexSample) int {
	lastBP := -1
	for i, v := range vs {
		if backpressured(v.Level) {
			lastBP = i
		}
	}
	if lastBP == -1 {
		return -1
	}
	best, bestBusy := -1, -2.0
	for i := lastBP + 1; i < len(vs); i++ {
		if backpressured(vs[i].Level) {
			continue
		}
		if vs[i].BusyMsPerSec > bestBusy {
			best, bestBusy = i, vs[i].BusyMsPerSec
		}
	}
	if best == -1 {
		return lastBP
	}
	return best
}
