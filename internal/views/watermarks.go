package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type wmRow struct {
	num      int
	name     string
	isSource bool
	effWM    int64 // effective (min-subtask) watermark epoch ms; 0 = none
	lagMs    int64 // wall clock - effWM; -1 when no watermark
	spread   int64 // max-min across subtasks (skew among inputs)
	idleMs   int64 // source idle time; -1 if n/a
}

type watermarksMsg struct {
	rows    []wmRow
	stall   int    // index of the first stalling vertex; -1 none
	verdict string // classified cause
	err     error
}

// Watermarks is the propagation debugger: it walks the DAG in topo order,
// shows each vertex's effective event-time watermark and lag, names the
// FIRST vertex where progress stalls, and classifies why — the "why isn't
// my window firing?" answer nothing else gives.
type Watermarks struct {
	deps Deps
	jid  string
	name string
	msg  watermarksMsg
}

// NewWatermarks builds the debugger for one job.
func NewWatermarks(deps Deps, jid, name string) *Watermarks {
	return &Watermarks{deps: deps, jid: jid, name: name}
}

func (w *Watermarks) Title() string   { return "watermarks" }
func (w *Watermarks) Filtering() bool { return false }
func (w *Watermarks) Bindings() []key.Binding {
	return []key.Binding{binding("r", "r", "refresh"), binding("esc", "esc", "back")}
}

func (w *Watermarks) Init() tea.Cmd { return w.Refresh() }

func (w *Watermarks) Refresh() tea.Cmd {
	cl := w.deps.CL
	jid := w.jid
	return func() tea.Msg {
		plan, err := cl.JobPlan(jid)
		if err != nil {
			return watermarksMsg{err: err}
		}
		job, err := cl.Job(jid)
		if err != nil {
			return watermarksMsg{err: err}
		}
		vName := map[string]string{}
		for _, v := range job.Vertices {
			vName[v.ID] = v.Name
		}
		ordered := topoOrder(plan.Nodes)
		now := time.Now().UnixMilli()
		rows := make([]wmRow, 0, len(ordered))
		for i, n := range ordered {
			nm := vName[n.ID]
			if nm == "" {
				nm = strings.ReplaceAll(n.Description, "<br/>", " ")
			}
			r := wmRow{num: i + 1, name: nm, isSource: len(n.Inputs) == 0, lagMs: -1, idleMs: -1}
			d, err := cl.VertexWatermarkDetail(jid, n.ID)
			if err == nil {
				r.idleMs = d.SourceIdle
				wm := effectiveWM(d)
				if wm > 0 {
					r.effWM = wm
					r.lagMs = now - wm
				}
				if len(d.Subtasks) >= 2 {
					r.spread = maxI64(d.Subtasks) - minI64(d.Subtasks)
				}
			}
			rows = append(rows, r)
		}
		stall, verdict := classifyWatermarks(rows)
		return watermarksMsg{rows: rows, stall: stall, verdict: verdict}
	}
}

// effectiveWM prefers the min input watermark; for sources it uses the
// output watermark.
func effectiveWM(d client.VertexWatermarkDetail) int64 {
	if len(d.Subtasks) > 0 {
		return minI64(d.Subtasks)
	}
	return d.OutputWM
}

// classifyWatermarks finds the first vertex whose lag jumps well past its
// upstream neighbor and explains why.
func classifyWatermarks(rows []wmRow) (int, string) {
	const bigLag = 60_000 // 1 min
	for i, r := range rows {
		if r.lagMs < 0 {
			if r.isSource && r.idleMs > 0 {
				return i, fmt.Sprintf("source %q is IDLE (%s) — no data arriving, so its watermark can't advance; set withIdleness() so it stops holding back the rest", short(r.name), model.FmtDuration(r.idleMs))
			}
			return i, fmt.Sprintf("vertex %q has no watermark yet — is a WatermarkStrategy assigned upstream?", short(r.name))
		}
		if r.lagMs < bigLag {
			continue
		}
		// this vertex is lagging; is its upstream healthy? (lag jumped here)
		upstreamOK := i == 0 || rows[i-1].lagMs >= 0 && rows[i-1].lagMs < bigLag
		if upstreamOK {
			if r.isSource && r.idleMs > 0 {
				return i, fmt.Sprintf("source %q is IDLE (%s) and holding the pipeline's watermark back", short(r.name), model.FmtDuration(r.idleMs))
			}
			if r.spread > bigLag {
				return i, fmt.Sprintf("vertex %q: one input subtask lags the others by %s — a skewed/idle upstream partition is pinning the min-watermark (per-subtask withIdleness or repartition)", short(r.name), model.FmtDuration(r.spread))
			}
			return i, fmt.Sprintf("vertex %q lags wall clock by %s while upstream is current — likely a large bounded-out-of-orderness/allowed-lateness, or this operator is slow", short(r.name), model.FmtDuration(r.lagMs))
		}
	}
	return -1, ""
}

func minI64(s []int64) int64 {
	m := s[0]
	for _, v := range s {
		if v < m {
			m = v
		}
	}
	return m
}
func maxI64(s []int64) int64 {
	m := s[0]
	for _, v := range s {
		if v > m {
			m = v
		}
	}
	return m
}
func short(s string) string {
	if len(s) > 34 {
		return s[:33] + "…"
	}
	return s
}

var _ = sort.Ints // topoOrder already imported via dagview

func (w *Watermarks) Update(msg tea.Msg) (Model, tea.Cmd) {
	if m, ok := msg.(watermarksMsg); ok {
		w.msg = m
	}
	return w, nil
}

func (w *Watermarks) View(width, height int) string {
	out := styles.ViewTitle().Render("Watermark propagation · "+w.name) +
		styles.Dim().Render("  event-time flow across the DAG · <r> refresh") + "\n"
	if w.msg.err != nil {
		return out + styles.ErrText().Render(" ⚠ "+w.msg.err.Error())
	}
	if len(w.msg.rows) == 0 {
		return out + styles.Dim().Render(" reading watermarks…")
	}
	var b strings.Builder
	b.WriteString(out + "\n")
	for i, r := range w.msg.rows {
		marker := "  "
		style := styles.Dim()
		lag := "—"
		if r.lagMs >= 0 {
			lag = model.FmtDuration(r.lagMs) + " behind"
			switch {
			case r.lagMs >= 60_000:
				style = styles.ErrText()
			case r.lagMs >= 10_000:
				style = styles.WarnText()
			default:
				style = styles.OKText()
			}
		} else {
			style = styles.ErrText()
			lag = "no watermark"
		}
		if i == w.msg.stall {
			marker = styles.ErrText().Render("▶ ")
		}
		extra := ""
		if r.isSource {
			extra = styles.Dim().Render("  [source]")
			if r.idleMs > 0 {
				extra += styles.WarnText().Render(fmt.Sprintf(" idle %s", model.FmtDuration(r.idleMs)))
			}
		}
		if r.spread > 10_000 {
			extra += styles.WarnText().Render(fmt.Sprintf("  subtask spread %s", model.FmtDuration(r.spread)))
		}
		b.WriteString(fmt.Sprintf(" %s#%-2d %-36s %s%s\n", marker, r.num, short(r.name), style.Render(lag), extra))
	}
	b.WriteString("\n")
	if w.msg.stall >= 0 {
		b.WriteString(" " + styles.ErrText().Render("⚑ stall: ") + w.msg.verdict + "\n")
	} else {
		b.WriteString(" " + styles.OKText().Render("● watermarks are flowing — no stall detected") + "\n")
	}
	return b.String()
}
