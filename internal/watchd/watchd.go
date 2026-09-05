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

// Collector polls a cluster headlessly and holds the latest verdicts.
type Collector struct {
	cl      *client.Client
	sent    *sentinel.Sentinel
	ctxName string
	period  time.Duration
	emit    func(Event)
	sinks   []sink.Sink

	mu      sync.RWMutex
	vgauge  map[string]vertexGauge // jid|vid -> gauge
	jgauge  map[string]jobGauge    // jid -> gauge
	lastRun map[string]int         // jid -> numRestarts
	scrape  time.Time
}

// New builds a collector. emit is called for every event (nil = discard).
func New(cl *client.Client, ctxName string, period time.Duration, emit func(Event)) *Collector {
	if emit == nil {
		emit = func(Event) {}
	}
	return &Collector{
		cl: cl, sent: sentinel.New(), ctxName: ctxName, period: period, emit: emit,
		vgauge: map[string]vertexGauge{}, jgauge: map[string]jobGauge{}, lastRun: map[string]int{},
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
		c.event(Event{Type: "error", Message: err.Error()})
		return
	}
	now := time.Now()
	vg := map[string]vertexGauge{}
	jg := map[string]jobGauge{}
	live := map[string]bool{}
	for _, j := range jobs {
		if j.State != "RUNNING" && j.State != "RESTARTING" && j.State != "FAILING" {
			continue
		}
		live[j.JID] = true
		job, err := c.cl.Job(j.JID)
		if err != nil {
			continue
		}
		jgObj := jobGauge{name: j.Name}
		// restart-loop detection
		if ms, err := c.cl.JobMetrics(j.JID, "numRestarts"); err == nil {
			for _, m := range ms {
				if m.ID == "numRestarts" {
					fmt.Sscanf(m.Value, "%d", &jgObj.restarts)
				}
			}
		}
		last, seen := c.lastRun[j.JID]
		c.lastRun[j.JID] = jgObj.restarts
		restarting := seen && jgObj.restarts > last
		if o, cl := c.sent.ObserveCondition(sentinel.Key{JID: j.JID, VID: "job"}, "restart-loop", j.Name, "job", restarting, now); o != nil {
			c.event(Event{Type: "alert_open", Kind: "restart-loop", Job: j.Name})
		} else if cl != nil {
			c.event(Event{Type: "alert_close", Kind: "restart-loop", Job: j.Name})
		}
		// checkpoint-failure streak
		ckptBad := false
		if cp, err := c.cl.Checkpoints(j.JID); err == nil && len(cp.History) >= 2 {
			ckptBad = cp.History[0].Status == "FAILED" && cp.History[1].Status == "FAILED"
		}
		if o, cl := c.sent.ObserveCondition(sentinel.Key{JID: j.JID, VID: "job"}, "checkpoint", j.Name, "checkpointing", ckptBad, now); o != nil {
			c.event(Event{Type: "alert_open", Kind: "checkpoint", Job: j.Name})
		} else if cl != nil {
			c.event(Event{Type: "alert_close", Kind: "checkpoint", Job: j.Name})
		}
		// per-vertex backpressure + skew
		var samples []analyze.VertexSample
		for _, v := range job.Vertices {
			level := ""
			if bp, err := c.cl.Backpressure(j.JID, v.ID); err == nil {
				level = bp.Level()
			}
			busy := -1.0
			if b, err := c.cl.VertexBusy(j.JID, v.ID); err == nil {
				busy = b
			}
			samples = append(samples, analyze.VertexSample{ID: v.ID, Name: v.Name, Level: level, BusyMsPerSec: busy, Parallelism: v.Parallelism})
			k := sentinel.Key{JID: j.JID, VID: v.ID}
			if o, cl := c.sent.Observe(k, j.Name, v.Name, sentinel.Sample{Time: now, Level: level, BusyMs: busy}); o != nil {
				c.event(Event{Type: "alert_open", Kind: "backpressure", Job: j.Name, Vertex: v.Name, Peak: o.Peak})
			} else if cl != nil {
				c.event(Event{Type: "alert_close", Kind: "backpressure", Job: j.Name, Vertex: v.Name})
			}
			skew := -1
			if v.Parallelism >= 2 {
				if recs, err := c.cl.VertexSubtaskRecords(j.JID, v.ID, v.Parallelism); err == nil && len(recs) > 0 {
					skew = analyze.SkewScore(recs)
					if o, cl := c.sent.ObserveSkew(k, j.Name, v.Name, skew, recs, now); o != nil {
						c.event(Event{Type: "alert_open", Kind: "skew", Job: j.Name, Vertex: v.Name, Peak: o.Peak})
					} else if cl != nil {
						c.event(Event{Type: "alert_close", Kind: "skew", Job: j.Name, Vertex: v.Name})
					}
				}
			}
			vg[j.JID+"|"+v.ID] = vertexGauge{job: j.Name, vertex: v.Name, bpLevel: levelCode(level), busy: busy, skew: skew}
		}
		jgObj.bottleneck = analyze.Bottleneck(samples) >= 0
		jg[j.JID] = jgObj
	}
	c.sent.Prune(live)
	// count open alerts per job
	for _, al := range c.sent.OpenAlerts() {
		for jid, g := range jg {
			if g.name == al.Job {
				g.openAlerts++
				jg[jid] = g
			}
		}
	}
	c.mu.Lock()
	c.vgauge, c.jgauge, c.scrape = vg, jg, now
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
	fmt.Fprintf(w, "# HELP f9s_up 1 when the collector last scraped the cluster successfully.\n# TYPE f9s_up gauge\nf9s_up{context=%q} 1\n", c.ctxName)
	fmt.Fprintf(w, "# HELP f9s_scrape_age_seconds Seconds since the last successful poll.\n# TYPE f9s_scrape_age_seconds gauge\nf9s_scrape_age_seconds{context=%q} %.1f\n", c.ctxName, time.Since(c.scrape).Seconds())

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
