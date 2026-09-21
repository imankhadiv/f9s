// Package metricts is f9s's in-memory metric time series: a bounded ring of
// samples per (scope, id, metric). Flink's REST API only answers "what is the
// value now"; every trend in f9s (autoscaler history, checkpoint growth, GC
// rate, pinned sparklines) is built from rings filled by the sampler.
package metricts

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// BufferLen is ~1h of history at the 15s sample interval (same as sentinel).
const BufferLen = 240

// Scopes mirror the Flink metric scopes f9s samples.
const (
	ScopeJM     = "jm"
	ScopeTM     = "tm"
	ScopeJob    = "job"
	ScopeVertex = "vertex"
	ScopePin    = "pin" // pinned Metrics Explorer series, ID = pins.SeriesID
)

// Metric names the sampler records. Flink metric names are used verbatim;
// the ckpt* ones are derived from the checkpoint history.
const (
	Busy         = "busyTimeMsPerSecond"   // vertex: max over subtasks
	RecordsIn    = "numRecordsInPerSecond" // vertex: sum, Subtasks = per-subtask
	CkptSize     = "ckptStateSize"         // job: bytes of the last completed checkpoint
	CkptDuration = "ckptDuration"          // job: end-to-end ms of the same checkpoint
)

// Key identifies one series. ID is the TM id for ScopeTM, the job id for
// ScopeJob, and "<jid>/<vid>" for ScopeVertex (see VertexID).
type Key struct {
	Scope  string
	ID     string
	Metric string
}

// VertexID builds the ID of a vertex-scoped series.
func VertexID(jid, vid string) string { return jid + "/" + vid }

// JobOf returns the job id a job- or vertex-scoped key belongs to ("" for
// cluster scopes).
func JobOf(k Key) string {
	switch k.Scope {
	case ScopeJob:
		return k.ID
	case ScopeVertex:
		if i := strings.IndexByte(k.ID, '/'); i >= 0 {
			return k.ID[:i]
		}
		return k.ID
	}
	return ""
}

// Point is one observation. Subtasks carries the per-subtask distribution
// when the sampler has it (vertex scope), nil otherwise.
type Point struct {
	Time     time.Time
	Value    float64
	Subtasks []float64
}

// Ring holds the series. Safe for concurrent use: the sampler writes from
// tea.Cmd goroutines while views read on the UI goroutine.
type Ring struct {
	mu   sync.Mutex
	size int
	buf  map[Key][]Point
}

// New builds a ring keeping at most size points per series (BufferLen when
// size <= 0).
func New(size int) *Ring {
	if size <= 0 {
		size = BufferLen
	}
	return &Ring{size: size, buf: map[Key][]Point{}}
}

// Add appends one point, dropping the oldest past the ring size. Points older
// than the series' newest, and NaN/Inf values, are ignored.
func (r *Ring) Add(k Key, p Point) {
	if r == nil || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
		return // Flink reports "NaN" for disabled metrics; it would poison JSON persistence
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.buf[k]
	if n := len(s); n > 0 && p.Time.Before(s[n-1].Time) {
		return
	}
	if p.Subtasks != nil {
		p.Subtasks = append([]float64(nil), p.Subtasks...)
	}
	s = append(s, p)
	if len(s) > r.size {
		s = append(s[:0:0], s[len(s)-r.size:]...)
	}
	r.buf[k] = s
}

// Series returns a copy of the series, oldest first.
func (r *Ring) Series(k Key) []Point {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Point(nil), r.buf[k]...)
}

// Values returns the series' values, oldest first (sparkline input).
func (r *Ring) Values(k Key) []float64 {
	pts := r.Series(k)
	out := make([]float64, len(pts))
	for i, p := range pts {
		out[i] = p.Value
	}
	return out
}

// Last returns the newest point.
func (r *Ring) Last(k Key) (Point, bool) {
	if r == nil {
		return Point{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.buf[k]
	if len(s) == 0 {
		return Point{}, false
	}
	return s[len(s)-1], true
}

// Keys lists the series in a scope ("" = all), sorted for stable output.
func (r *Ring) Keys(scope string) []Key {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Key
	for k := range r.buf {
		if scope == "" || k.Scope == scope {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Metric < b.Metric
	})
	return out
}

// Prune drops every series whose key keep rejects (e.g. jobs that stopped
// running, TMs that left the cluster) so the map can't grow without bound.
func (r *Ring) Prune(keep func(Key) bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.buf {
		if !keep(k) {
			delete(r.buf, k)
		}
	}
}

// Rate turns a cumulative counter series (GC counts, numRecordsIn, …) into a
// per-second rate between consecutive points. A negative delta is a counter
// reset (restart, new TM) and yields no rate for that interval.
func Rate(pts []Point) []float64 {
	var out []float64
	for i := 1; i < len(pts); i++ {
		dt := pts[i].Time.Sub(pts[i-1].Time).Seconds()
		dv := pts[i].Value - pts[i-1].Value
		if dt <= 0 || dv < 0 {
			continue
		}
		out = append(out, dv/dt)
	}
	return out
}

// Delta is the change of a cumulative counter across the whole series,
// summing only non-negative steps so a reset mid-window doesn't cancel the
// history before it.
func Delta(pts []Point) float64 {
	var sum float64
	for i := 1; i < len(pts); i++ {
		if dv := pts[i].Value - pts[i-1].Value; dv > 0 {
			sum += dv
		}
	}
	return sum
}

// Slope is the least-squares trend of the series in value units per second;
// ok is false with fewer than 3 points or no time spread.
func Slope(pts []Point) (perSec float64, ok bool) {
	if len(pts) < 3 {
		return 0, false
	}
	t0 := pts[0].Time
	var sx, sy, sxx, sxy float64
	n := float64(len(pts))
	for _, p := range pts {
		x := p.Time.Sub(t0).Seconds()
		sx += x
		sy += p.Value
		sxx += x * x
		sxy += x * p.Value
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, false
	}
	return (n*sxy - sx*sy) / den, true
}

// Since returns the points at or after t (the series is time-ordered).
func Since(pts []Point, t time.Time) []Point {
	i := sort.Search(len(pts), func(i int) bool { return !pts[i].Time.Before(t) })
	return pts[i:]
}
