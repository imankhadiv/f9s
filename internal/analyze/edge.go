package analyze

// EdgeVerdict classifies where a vertex's backpressure actually originates,
// from the per-subtask network buffer pools and the busy trio. The coarse
// per-vertex backpressure LEVEL says "this vertex is backpressured" but not
// which side: a full OUTPUT pool means the downstream is slow (this vertex is a
// victim), a full INPUT pool means the upstream is slow (this vertex is
// starved), and high busy with clear pools means this vertex is itself the
// CPU-bound constraint. That distinction is the whole point of the compare view.
type EdgeVerdict struct {
	// Kind is one of: "downstream", "upstream", "cpu", "idle", "ok".
	Kind    string
	Summary string
	// HotSubtasks are the subtask indices most implicated (for highlighting).
	HotSubtasks []int
}

// poolHigh / busyHigh are the thresholds at which a signal is "saturated".
const (
	poolHigh = 0.85 // buffer-pool usage 0..1
	busyHigh = 800  // busyTimeMsPerSecond, out of ~1000
)

// BackpressureEdge reads the per-subtask signals and returns a verdict. All
// slices are indexed by subtask; empty or mismatched inputs yield an "ok"
// verdict rather than a guess.
func BackpressureEdge(busy, inPool, outPool []float64) EdgeVerdict {
	n := len(busy)
	if n == 0 || len(inPool) != n || len(outPool) != n {
		return EdgeVerdict{Kind: "ok", Summary: "no per-subtask signal"}
	}
	var outHot, inHot, busyHot []int
	for i := 0; i < n; i++ {
		if outPool[i] >= poolHigh {
			outHot = append(outHot, i)
		}
		if inPool[i] >= poolHigh {
			inHot = append(inHot, i)
		}
		if busy[i] >= busyHigh {
			busyHot = append(busyHot, i)
		}
	}
	switch {
	case len(outHot) > 0:
		// Output buffers backing up: downstream can't keep up. This vertex is a
		// victim, not the cause — the fix is downstream (rescale/repartition it).
		return EdgeVerdict{
			Kind:        "downstream",
			Summary:     "output buffers full — the DOWNSTREAM operator is the constraint; this vertex is backpressured waiting on it",
			HotSubtasks: outHot,
		}
	case len(inHot) > 0 && len(busyHot) == 0:
		// Input starved without being busy: upstream isn't feeding fast enough.
		return EdgeVerdict{
			Kind:        "upstream",
			Summary:     "input pools starved while idle — the UPSTREAM operator (or source) is the constraint",
			HotSubtasks: inHot,
		}
	case len(busyHot) > 0:
		// Busy with clear output: this vertex is itself CPU-bound.
		return EdgeVerdict{
			Kind:        "cpu",
			Summary:     "high busy-time with clear output buffers — THIS vertex is CPU-bound; rescale it up",
			HotSubtasks: busyHot,
		}
	}
	// Nothing saturated. Distinguish healthy-working from mostly-idle.
	allIdle := true
	for i := 0; i < n; i++ {
		if busy[i] >= 100 {
			allIdle = false
			break
		}
	}
	if allIdle {
		return EdgeVerdict{Kind: "idle", Summary: "mostly idle — not the bottleneck"}
	}
	return EdgeVerdict{Kind: "ok", Summary: "flowing — no buffer saturation"}
}
