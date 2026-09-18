// Package watchd is f9s's headless collector: it runs the same detectors as
// the TUI (backpressure, skew, checkpoint-failure, restart-loop) with no
// terminal, emitting NDJSON events and exposing computed verdicts as
// Prometheus metrics. f9s's brains, usable by anything — jq, Grafana,
// Alertmanager, a shell one-liner.
package watchd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"context"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/sentinel"
	"github.com/imankhadiv/f9s/internal/sink"
)

// Event is one NDJSON line: an alert transition or a periodic heartbeat.
type Event struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"` // alert_open | alert_close | heartbeat | error
	Kind    string    `json:"kind,omitempty"`
	Job     string    `json:"job,omitempty"`
	Vertex  string    `json:"vertex,omitempty"`
	Peak    string    `json:"peak,omitempty"`
	Context string    `json:"context"`
	Message string    `json:"message,omitempty"`
	Schema  int       `json:"schemaVersion"`
}

const schemaVersion = 1

// SchemaVersion returns the event schema version.
func (e Event) SchemaVersion() int { return e.Schema }

// vertexGauge is the live verdict for one vertex.
type vertexGauge struct {
	job, vertex string
	bpLevel     int // 0 ok, 1 low, 2 high, -1 unknown
	busy        float64
	skew        int
}

// jobGauge is the live verdict for one job.
type jobGauge struct {
	name       string
	restarts   int
	bottleneck bool // this job HAS a computed bottleneck
	openAlerts int
}

type vertexObservation struct {
	id, name   string
	gauge      vertexGauge
	sample     analyze.VertexSample
	bpKnown    bool
	skewKnown  bool
	skew       int
	skewValues []float64
}

type jobObservation struct {
	jid, name       string
	gauge           jobGauge
	restartsKnown   bool
	checkpointBad   bool
	checkpointKnown bool
	vertices        []vertexObservation
}

// Collector polls a cluster headlessly and holds the latest verdicts.
type Collector struct {
	cl      *client.Client
	sent    *sentinel.Sentinel
	ctxName string
	period  time.Duration
	emit    func(Event)
	sinks   []sink.Sink

	mu          sync.RWMutex
	vgauge      map[string]vertexGauge // jid|vid -> gauge
	jgauge      map[string]jobGauge    // jid -> gauge
	lastRun     map[string]int         // jid -> numRestarts
	lastState   map[string]string      // jid -> state from last authoritative poll
	up          bool
	lastSuccess time.Time
}

// New builds a collector. emit is called for every event (nil = discard).
func New(cl *client.Client, ctxName string, period time.Duration, emit func(Event)) *Collector {
	if emit == nil {
		emit = func(Event) {}
	}
	return &Collector{
		cl: cl, sent: sentinel.New(), ctxName: ctxName, period: period, emit: emit,
		vgauge: map[string]vertexGauge{}, jgauge: map[string]jobGauge{},
		lastRun: map[string]int{}, lastState: map[string]string{},
	}
}

// WithSinks attaches alert sinks; opened/cleared events are forwarded to
// each in addition to the NDJSON emitter.
func (c *Collector) WithSinks(s []sink.Sink) *Collector { c.sinks = s; return c }

// Run polls until stop is closed.
func (c *Collector) Run(stop <-chan struct{}) {
	t := time.NewTicker(c.period)
	defer t.Stop()
	c.poll()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			c.poll()
		}
	}
}

func (c *Collector) event(e Event) {
	e.Time = time.Now()
	e.Context = c.ctxName
	e.Schema = schemaVersion
	c.emit(e)
	if len(c.sinks) > 0 && (e.Type == "alert_open" || e.Type == "alert_close") {
		go sink.Dispatch(context.Background(), c.sinks, sink.Event{
			Type: e.Type, Kind: e.Kind, Job: e.Job, Vertex: e.Vertex,
			Peak: e.Peak, Context: e.Context, Time: e.Time,
		})
	}
}

func (c *Collector) poll() {
	jobs, err := c.cl.Jobs()
	if err != nil {
		c.markPollFailed()
		c.event(Event{Type: "error", Message: err.Error()})
		return
	}
	now := time.Now()
	observations := make([]jobObservation, 0, len(jobs))
	live := map[string]bool{}
	unknown := map[string]bool{}
	complete := true

	// Fetch all required job details before changing detector or gauge state.
	// A partial sweep must not make missing jobs or vertices look healthy —
	// but one unreadable job must not blind the rest of the cluster either,
	// so it is skipped rather than aborting the sweep. Its detectors go
	// unobserved and its last-good gauges are carried forward below.
	for _, j := range jobs {
		if !monitoredState(j.State) {
			continue
		}
		live[j.JID] = true
		job, err := c.cl.Job(j.JID)
		if err != nil {
			complete = false
			unknown[j.JID] = true
			c.event(Event{Type: "error", Job: j.Name, Message: fmt.Sprintf("job detail: %v", err)})
			continue
		}

		obs := jobObservation{
			jid:   j.JID,
			name:  j.Name,
			gauge: jobGauge{name: j.Name, restarts: -1},
		}
		if ms, err := c.cl.JobMetrics(j.JID, "numRestarts"); err == nil {
			for _, m := range ms {
				if m.ID != "numRestarts" {
					continue
				}
				if _, err := fmt.Sscanf(m.Value, "%d", &obs.gauge.restarts); err == nil {
					obs.restartsKnown = true
				}
				break
			}
		}
		if cp, err := c.cl.Checkpoints(j.JID); err == nil {
			obs.checkpointKnown = true
			obs.checkpointBad = len(cp.History) >= 2 &&
				cp.History[0].Status == "FAILED" && cp.History[1].Status == "FAILED"
		}

		var samples []analyze.VertexSample
		for _, v := range job.Vertices {
			level := ""
			if bp, err := c.cl.Backpressure(j.JID, v.ID); err == nil {
				level = strings.ToLower(bp.Level())
			}
			busy := -1.0
			if b, err := c.cl.VertexBusy(j.JID, v.ID); err == nil {
				busy = b
			}
			sample := analyze.VertexSample{
				ID: v.ID, Name: v.Name, Level: level,
				BusyMsPerSec: busy, Parallelism: v.Parallelism,
			}
			samples = append(samples, sample)

			vo := vertexObservation{
				id: v.ID, name: v.Name, sample: sample,
				bpKnown: levelCode(level) >= 0,
				skew:    -1,
				gauge: vertexGauge{
					job: j.Name, vertex: v.Name, bpLevel: levelCode(level),
					busy: busy, skew: -1,
				},
			}
			if v.Parallelism >= 2 {
				if recs, err := c.cl.VertexSubtaskRecords(j.JID, v.ID, v.Parallelism); err == nil && len(recs) > 0 {
					vo.skew = analyze.SkewScore(recs)
					vo.skewKnown = true
					vo.skewValues = recs
					vo.gauge.skew = vo.skew
				}
			}
			obs.vertices = append(obs.vertices, vo)
		}
		obs.gauge.bottleneck = analyze.Bottleneck(samples) >= 0
		observations = append(observations, obs)
	}

	vg := map[string]vertexGauge{}
	jg := map[string]jobGauge{}
	lastRun := map[string]int{}
	for _, obs := range observations {
		if last, ok := c.lastRun[obs.jid]; ok {
			lastRun[obs.jid] = last
		}
		if obs.restartsKnown {
			last, seen := c.lastRun[obs.jid]
			lastRun[obs.jid] = obs.gauge.restarts
			restarting := seen && obs.gauge.restarts > last
			if o, cl := c.sent.ObserveCondition(sentinel.Key{JID: obs.jid, VID: "job"}, "restart-loop", obs.name, "job", restarting, now); o != nil {
				c.event(Event{Type: "alert_open", Kind: "restart-loop", Job: obs.name})
			} else if cl != nil {
				c.event(Event{Type: "alert_close", Kind: "restart-loop", Job: obs.name})
			}
		}
		if obs.checkpointKnown {
			if o, cl := c.sent.ObserveCondition(sentinel.Key{JID: obs.jid, VID: "job"}, "checkpoint", obs.name, "checkpointing", obs.checkpointBad, now); o != nil {
				c.event(Event{Type: "alert_open", Kind: "checkpoint", Job: obs.name})
			} else if cl != nil {
				c.event(Event{Type: "alert_close", Kind: "checkpoint", Job: obs.name})
			}
		}
		for _, v := range obs.vertices {
			k := sentinel.Key{JID: obs.jid, VID: v.id}
			if v.bpKnown {
				if o, cl := c.sent.Observe(k, obs.name, v.name, sentinel.Sample{Time: now, Level: v.sample.Level, BusyMs: v.sample.BusyMsPerSec}); o != nil {
					c.event(Event{Type: "alert_open", Kind: "backpressure", Job: obs.name, Vertex: v.name, Peak: o.Peak})
				} else if cl != nil {
					c.event(Event{Type: "alert_close", Kind: "backpressure", Job: obs.name, Vertex: v.name})
				}
			}
			if v.skewKnown {
				if o, cl := c.sent.ObserveSkew(k, obs.name, v.name, v.skew, v.skewValues, now); o != nil {
					c.event(Event{Type: "alert_open", Kind: "skew", Job: obs.name, Vertex: v.name, Peak: o.Peak})
				} else if cl != nil {
					c.event(Event{Type: "alert_close", Kind: "skew", Job: obs.name, Vertex: v.name})
				}
			}
			vg[obs.jid+"|"+v.id] = v.gauge
		}
		jg[obs.jid] = obs.gauge
	}
	// A job we could not read this sweep keeps its last-good gauges and its
	// restart baseline, so its series neither vanish nor reset. openAlerts is
	// recounted below, so clear the carried-over tally first.
	for jid := range unknown {
		if g, ok := c.jgauge[jid]; ok {
			g.openAlerts = 0
			jg[jid] = g
		}
		if last, ok := c.lastRun[jid]; ok {
			lastRun[jid] = last
		}
		for k, g := range c.vgauge {
			if strings.HasPrefix(k, jid+"|") {
				vg[k] = g
			}
		}
	}

	c.sent.Prune(live)
	// count open alerts per job
	for _, al := range c.sent.OpenAlerts() {
		if g, ok := jg[al.Key.JID]; ok {
			g.openAlerts++
			jg[al.Key.JID] = g
		}
	}

	states := make(map[string]string, len(jobs))
	for _, j := range jobs {
		states[j.JID] = j.State
		if previous, seen := c.lastState[j.JID]; seen {
			switch {
			case previous != "FAILED" && j.State == "FAILED":
				c.event(Event{Type: "alert_open", Kind: "job-failed", Job: j.Name})
			case previous == "FAILED" && j.State != "FAILED":
				c.event(Event{Type: "alert_close", Kind: "job-failed", Job: j.Name})
			}
		}
	}

	c.mu.Lock()
	c.vgauge, c.jgauge = vg, jg
	c.lastRun, c.lastState = lastRun, states
	c.up = complete
	if complete {
		c.lastSuccess = now
	}
	c.mu.Unlock()
}

func monitoredState(state string) bool {
	return state == "RUNNING" || state == "RESTARTING" || state == "FAILING"
}

func (c *Collector) markPollFailed() {
	c.mu.Lock()
	c.up = false
	c.mu.Unlock()
}

func levelCode(l string) int {
	switch strings.ToLower(l) {
	case "ok":
		return 0
	case "low":
		return 1
	case "high":
		return 2
	}
	return -1
}

// WriteMetrics renders the current verdicts in Prometheus exposition format.
func (c *Collector) WriteMetrics(w io.Writer) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	fmt.Fprintf(w, "# HELP f9s_up 1 when the latest core cluster poll completed successfully.\n# TYPE f9s_up gauge\nf9s_up{context=%q} %d\n", c.ctxName, b2i(c.up))
	fmt.Fprint(w, "# HELP f9s_scrape_age_seconds Seconds since the last successful core cluster poll.\n# TYPE f9s_scrape_age_seconds gauge\n")
	if !c.lastSuccess.IsZero() {
		fmt.Fprintf(w, "f9s_scrape_age_seconds{context=%q} %.1f\n", c.ctxName, time.Since(c.lastSuccess).Seconds())
	}

	fmt.Fprint(w, "# HELP f9s_vertex_skew_percent f9s's computed FLIP-418 skew score per vertex.\n# TYPE f9s_vertex_skew_percent gauge\n")
	fmt.Fprint(w, "# HELP f9s_vertex_backpressure_level Backpressure level: 0 ok, 1 low, 2 high.\n# TYPE f9s_vertex_backpressure_level gauge\n")
	fmt.Fprint(w, "# HELP f9s_vertex_busy_ms_per_sec busyTimeMsPerSecond per vertex.\n# TYPE f9s_vertex_busy_ms_per_sec gauge\n")
	vkeys := sortedKeys(c.vgauge)
	for _, k := range vkeys {
		g := c.vgauge[k]
		lbl := fmt.Sprintf("{context=%q,job=%q,vertex=%q}", c.ctxName, g.job, g.vertex)
		if g.skew >= 0 {
			fmt.Fprintf(w, "f9s_vertex_skew_percent%s %d\n", lbl, g.skew)
		}
		if g.bpLevel >= 0 {
			fmt.Fprintf(w, "f9s_vertex_backpressure_level%s %d\n", lbl, g.bpLevel)
		}
		if g.busy >= 0 {
			fmt.Fprintf(w, "f9s_vertex_busy_ms_per_sec%s %.0f\n", lbl, g.busy)
		}
	}

	fmt.Fprint(w, "# HELP f9s_job_restarts Total restarts of a running job.\n# TYPE f9s_job_restarts gauge\n")
	fmt.Fprint(w, "# HELP f9s_job_has_bottleneck 1 when f9s computes a bottleneck vertex for the job.\n# TYPE f9s_job_has_bottleneck gauge\n")
	fmt.Fprint(w, "# HELP f9s_job_open_alerts Open f9s alerts for the job.\n# TYPE f9s_job_open_alerts gauge\n")
	for _, jid := range sortedKeys(c.jgauge) {
		g := c.jgauge[jid]
		lbl := fmt.Sprintf("{context=%q,job=%q}", c.ctxName, g.name)
		if g.restarts >= 0 {
			fmt.Fprintf(w, "f9s_job_restarts%s %d\n", lbl, g.restarts)
		}
		fmt.Fprintf(w, "f9s_job_has_bottleneck%s %d\n", lbl, b2i(g.bottleneck))
		fmt.Fprintf(w, "f9s_job_open_alerts%s %d\n", lbl, g.openAlerts)
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// EncodeNDJSON is the default emitter: one JSON object per line to w.
func EncodeNDJSON(w io.Writer) func(Event) {
	enc := json.NewEncoder(w)
	var mu sync.Mutex
	return func(e Event) {
		mu.Lock()
		_ = enc.Encode(e)
		mu.Unlock()
	}
}
