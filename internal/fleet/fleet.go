// Package fleet samples every JobManager/TaskManager JVM into the metric
// ring and turns the history into per-JVM health verdicts: heap, GC time,
// metaspace, managed memory and network buffers — the resources that decide
// whether a TaskManager is about to OOM or thrash long before a job fails.
package fleet

import (
	"fmt"
	"sort"
	"time"

	"github.com/imankhadiv/f9s/internal/metricts"
	"github.com/imankhadiv/f9s/internal/model"
)

// JMID is the ring id of the JobManager series.
const JMID = "jobmanager"

// Metric names recorded per JVM (ScopeJM / ScopeTM).
const (
	HeapUsed     = "heapUsed"
	HeapMax      = "heapMax"
	MetaUsed     = "metaspaceUsed"
	MetaMax      = "metaspaceMax"
	CPU          = "cpuLoad"
	GCTime       = "gcTimeMs" // cumulative
	GCCount      = "gcCount"  // cumulative
	ManagedUsed  = "managedUsed"
	ManagedTotal = "managedTotal"
	NetUsed      = "networkSegmentsUsed"
	NetTotal     = "networkSegmentsTotal"
)

// Source is what Collect needs from the REST client.
type Source interface {
	TaskManagers() ([]model.TaskManager, error)
	JVMStats(path string) (model.JVMStats, error)
}

// Collect samples the JobManager and every TaskManager into the ring and
// returns the ids of the TaskManagers seen (nil when the TM list failed, so
// the caller doesn't prune history on a transient error).
func Collect(src Source, ring *metricts.Ring, now time.Time) map[string]bool {
	if st, err := src.JVMStats("/jobmanager/metrics"); err == nil {
		record(ring, metricts.ScopeJM, JMID, st, now)
	}
	tms, err := src.TaskManagers()
	if err != nil {
		return nil
	}
	live := make(map[string]bool, len(tms))
	for _, tm := range tms {
		live[tm.ID] = true
		if st, err := src.JVMStats("/taskmanagers/" + tm.ID + "/metrics"); err == nil {
			record(ring, metricts.ScopeTM, tm.ID, st, now)
		}
	}
	return live
}

func record(ring *metricts.Ring, scope, id string, st model.JVMStats, now time.Time) {
	add := func(metric string, v float64) {
		if v >= 0 {
			ring.Add(metricts.Key{Scope: scope, ID: id, Metric: metric}, metricts.Point{Time: now, Value: v})
		}
	}
	add(HeapUsed, st.HeapUsed)
	add(HeapMax, st.HeapMax)
	add(MetaUsed, st.MetaUsed)
	add(MetaMax, st.MetaMax)
	add(CPU, st.CPULoad)
	add(GCTime, st.GCTimeMs)
	add(GCCount, st.GCCount)
	add(ManagedUsed, st.ManagedUsed)
	add(ManagedTotal, st.ManagedTotal)
	if st.NetTotal > 0 && st.NetAvail >= 0 {
		add(NetUsed, st.NetTotal-st.NetAvail)
		add(NetTotal, st.NetTotal)
	}
}

// Row is one JVM's current resource picture. Fractions are -1 when unknown.
type Row struct {
	Scope, ID  string
	Heap       float64   // used/max, latest
	HeapTrend  []float64 // heap used/max over the window, for a sparkline
	GCMsPerSec float64   // GC time per wall second over the window (-1 unknown)
	GCTrend    []float64 // per-interval GC ms/s
	Meta       float64
	Managed    float64
	Network    float64
	CPU        float64
	Verdicts   []Verdict
}

// Verdict is one flagged condition on a JVM.
type Verdict struct {
	Level string // warn / crit
	Text  string
}

// Window is how much history the rates and trends look at.
const Window = 5 * time.Minute

// Rows builds the rows for every JVM in the ring: the JobManager first, then
// TaskManagers by id.
func Rows(ring *metricts.Ring, now time.Time) []Row {
	var out []Row
	seen := map[string]bool{}
	for _, k := range ring.Keys("") {
		if k.Scope != metricts.ScopeJM && k.Scope != metricts.ScopeTM {
			continue
		}
		id := k.Scope + "/" + k.ID
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, rowOf(ring, k.Scope, k.ID, now))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope == metricts.ScopeJM
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func rowOf(ring *metricts.Ring, scope, id string, now time.Time) Row {
	series := func(m string) []metricts.Point {
		return metricts.Since(ring.Series(metricts.Key{Scope: scope, ID: id, Metric: m}), now.Add(-Window))
	}
	latest := func(m string) float64 {
		if p, ok := ring.Last(metricts.Key{Scope: scope, ID: id, Metric: m}); ok {
			return p.Value
		}
		return -1
	}
	r := Row{Scope: scope, ID: id,
		Heap:    model.Frac(latest(HeapUsed), latest(HeapMax)),
		Meta:    model.Frac(latest(MetaUsed), latest(MetaMax)),
		Managed: model.Frac(latest(ManagedUsed), latest(ManagedTotal)),
		Network: model.Frac(latest(NetUsed), latest(NetTotal)),
		CPU:     latest(CPU), GCMsPerSec: -1}

	hu, hm := series(HeapUsed), series(HeapMax)
	for i := range hu {
		if i < len(hm) && hm[i].Value > 0 {
			r.HeapTrend = append(r.HeapTrend, hu[i].Value/hm[i].Value)
		}
	}
	gc := series(GCTime)
	r.GCTrend = metricts.Rate(gc)
	if len(gc) >= 2 {
		if span := gc[len(gc)-1].Time.Sub(gc[0].Time).Seconds(); span > 0 {
			r.GCMsPerSec = metricts.Delta(gc) / span
		}
	}
	r.Verdicts = verdicts(r)
	return r
}

// verdicts flags the conditions that precede TaskManager loss.
func verdicts(r Row) []Verdict {
	var out []Verdict
	switch {
	case r.GCMsPerSec >= 300:
		out = append(out, Verdict{"crit", fmt.Sprintf("GC thrashing: %.0f%% of wall time in GC", r.GCMsPerSec/10)})
	case r.GCMsPerSec >= 100:
		out = append(out, Verdict{"warn", fmt.Sprintf("GC pressure: %.0f%% of wall time in GC", r.GCMsPerSec/10)})
	}
	if r.Heap >= 0.9 {
		lvl := "warn"
		if r.GCMsPerSec >= 100 {
			lvl = "crit" // full heap AND collecting hard: an OOM is near
		}
		out = append(out, Verdict{lvl, fmt.Sprintf("heap %.0f%% full", r.Heap*100)})
	}
	if r.Meta >= 0.9 {
		out = append(out, Verdict{"warn", fmt.Sprintf("metaspace %.0f%% — classloader leak? (repeated job submits on a session cluster)", r.Meta*100)})
	}
	if r.Network >= 0.95 {
		out = append(out, Verdict{"warn", "network buffers exhausted — downstream backpressure or too few buffers"})
	}
	return out
}
