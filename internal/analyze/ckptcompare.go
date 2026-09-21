package analyze

import (
	"sort"

	"github.com/imankhadiv/f9s/internal/model"
)

// CkptMetric selects which per-vertex number a comparison looks at.
type CkptMetric int

// The comparable per-vertex checkpoint metrics.
const (
	CkptStateSize CkptMetric = iota
	CkptDuration
	CkptUploaded // checkpointed_size: what the checkpoint actually wrote
	CkptAlignment
	ckptMetrics
)

// Next cycles to the following metric.
func (m CkptMetric) Next() CkptMetric { return (m + 1) % ckptMetrics }

func (m CkptMetric) String() string {
	return [...]string{"state size", "duration", "uploaded", "alignment buffered"}[m]
}

// IsBytes reports whether the metric is a byte count (else milliseconds).
func (m CkptMetric) IsBytes() bool { return m != CkptDuration }

func (m CkptMetric) of(t model.CheckpointTaskStats) int64 {
	switch m {
	case CkptDuration:
		return t.EndToEndDuration
	case CkptUploaded:
		return t.CheckpointedSize
	case CkptAlignment:
		return t.AlignmentBuffered
	}
	return t.StateSize
}

// VertexCkptTrend is one vertex's metric across the compared checkpoints.
type VertexCkptTrend struct {
	VID    string
	Values []int64 // oldest → newest; -1 where the vertex is missing
	Change float64 // newest vs oldest (0.5 = +50%); 0 when either is missing/zero
	Delta  int64   // newest − oldest
}

// CompareCheckpoints lines up per-vertex numbers across checkpoints
// (details newest first, as the history lists them) and sorts the vertices
// by absolute growth, biggest first — the operator most responsible for a
// checkpoint getting bigger or slower rises to the top.
func CompareCheckpoints(details []model.CheckpointDetail, m CkptMetric) []VertexCkptTrend {
	n := len(details)
	vids := map[string]bool{}
	for _, d := range details {
		for vid := range d.Tasks {
			vids[vid] = true
		}
	}
	out := make([]VertexCkptTrend, 0, len(vids))
	for vid := range vids {
		tr := VertexCkptTrend{VID: vid, Values: make([]int64, n)}
		for i, d := range details {
			v := int64(-1)
			if t, ok := d.Tasks[vid]; ok {
				v = m.of(t)
			}
			tr.Values[n-1-i] = v // oldest first
		}
		first, last := tr.Values[0], tr.Values[n-1]
		if first >= 0 && last >= 0 {
			tr.Delta = last - first
			if first > 0 {
				tr.Change = float64(last-first) / float64(first)
			}
		}
		out = append(out, tr)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Delta != out[j].Delta {
			return out[i].Delta > out[j].Delta
		}
		return out[i].VID < out[j].VID
	})
	return out
}
