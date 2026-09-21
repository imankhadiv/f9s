package analyze

import (
	"fmt"
	"sort"
	"time"
)

// ScalingEvent is one operator ScalingReport: when, on which resource, and
// the per-vertex parallelism changes it carried.
type ScalingEvent struct {
	At       time.Time
	Resource string // namespace/name
	Changes  []ScalingChange
}

// Flap is a vertex whose autoscaled parallelism keeps reversing direction.
type Flap struct {
	Resource  string
	VID       string
	Reversals int
	At        time.Time // the change that crossed the threshold
	Path      []int     // parallelism sequence inside the window
}

func (f Flap) String() string {
	return fmt.Sprintf("%s vertex %s reversed direction %d× (%v)", f.Resource, shortID(f.VID), f.Reversals, f.Path)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Flapping finds vertices whose scaling direction reversed at least twice
// (up→down→up or down→up→down) within window — the autoscaler chasing its
// own tail, usually a too-narrow utilization boundary or too-short
// stabilization/scale-down intervals.
func Flapping(evs []ScalingEvent, window time.Duration) []Flap {
	type step struct {
		at       time.Time
		from, to int
	}
	series := map[[2]string][]step{}
	for _, e := range evs {
		for _, c := range e.Changes {
			if c.To == c.From {
				continue
			}
			k := [2]string{e.Resource, c.VID}
			series[k] = append(series[k], step{e.At, c.From, c.To})
		}
	}
	var out []Flap
	for k, steps := range series {
		sort.Slice(steps, func(i, j int) bool { return steps[i].at.Before(steps[j].at) })
		start := 0
		for end := range steps {
			for steps[end].at.Sub(steps[start].at) > window {
				start++
			}
			rev := 0
			for i := start + 1; i <= end; i++ {
				if (steps[i].to > steps[i].from) != (steps[i-1].to > steps[i-1].from) {
					rev++
				}
			}
			if rev >= 2 {
				path := []int{steps[start].from}
				for i := start; i <= end; i++ {
					path = append(path, steps[i].to)
				}
				out = append(out, Flap{Resource: k[0], VID: k[1], Reversals: rev, At: steps[end].at, Path: path})
				break // one verdict per vertex
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}
