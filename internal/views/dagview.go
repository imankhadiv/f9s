package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/sentinel"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type edgeRate struct {
	recs, bytes float64
}

type dagMsg struct {
	plan  model.Plan
	job   model.Job
	rates map[string]edgeRate // downstream vertex id → inbound shuffle rate
	err   error
}

// DagView paints the job's execution graph as an ASCII DAG with live
// busy/backpressure state on every node — topology the Web UI shows only
// as an SVG you can't grep.
type DagView struct {
	deps  Deps
	jid   string
	name  string
	plan  model.Plan
	job   model.Job
	rates map[string]edgeRate
	vp    viewport.Model
	ready bool
	err   string
}

// NewDagView builds the DAG view for one job.
func NewDagView(deps Deps, jid, name string) *DagView {
	return &DagView{deps: deps, jid: jid, name: name}
}

func (d *DagView) Title() string   { return "dag(" + d.name + ")" }
func (d *DagView) Filtering() bool { return false }
func (d *DagView) Bindings() []key.Binding {
	return []key.Binding{
		binding("up", "↑↓", "scroll"),
		binding("esc", "esc", "back"),
	}
}

func (d *DagView) Init() tea.Cmd { return d.Refresh() }

func (d *DagView) Refresh() tea.Cmd {
	cl := d.deps.CL
	jid := d.jid
	return func() tea.Msg {
		plan, err := cl.JobPlan(jid)
		if err != nil {
			return dagMsg{err: err}
		}
		job, err := cl.Job(jid)
		if err != nil {
			return dagMsg{err: err}
		}
		rates := map[string]edgeRate{}
		for _, n := range plan.Nodes {
			if len(n.Inputs) == 0 {
				continue // sources have no inbound edge
			}
			if r, by, rerr := cl.VertexInRates(jid, n.ID); rerr == nil {
				rates[n.ID] = edgeRate{recs: r, bytes: by}
			}
		}
		return dagMsg{plan: plan, job: job, rates: rates, err: nil}
	}
}

func (d *DagView) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dagMsg:
		if msg.err != nil {
			d.err = msg.err.Error()
			return d, nil
		}
		d.err = ""
		d.plan = msg.plan
		d.job = msg.job
		d.rates = msg.rates
		if d.ready {
			d.vp.SetContent(d.render())
		}
		return d, nil
	case tea.KeyMsg:
		if d.ready {
			var cmd tea.Cmd
			d.vp, cmd = d.vp.Update(msg)
			return d, cmd
		}
	}
	return d, nil
}

// topoOrder returns plan nodes in topological order (sources first), with a
// stable tie-break so the layout doesn't jump between refreshes.
func topoOrder(nodes []model.PlanNode) []model.PlanNode {
	indeg := map[string]int{}
	for _, n := range nodes {
		indeg[n.ID] += 0
		for range n.Inputs {
			indeg[n.ID]++
		}
	}
	remaining := append([]model.PlanNode(nil), nodes...)
	var out []model.PlanNode
	for len(remaining) > 0 {
		var ready []model.PlanNode
		var rest []model.PlanNode
		for _, n := range remaining {
			if indeg[n.ID] == 0 {
				ready = append(ready, n)
			} else {
				rest = append(rest, n)
			}
		}
		if len(ready) == 0 { // cycle safety: dump the rest as-is
			out = append(out, rest...)
			break
		}
		sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
		for _, n := range ready {
			out = append(out, n)
			for _, m := range rest {
				for _, in := range m.Inputs {
					if in.ID == n.ID {
						indeg[m.ID]--
					}
				}
			}
		}
		remaining = rest
	}
	return out
}

// nodeLabel prefers the runtime vertex name over the plan description
// (which embeds <br/> chain markup).
func (d *DagView) nodeLabel(n model.PlanNode) (label string, parallelism int) {
	for _, v := range d.job.Vertices {
		if v.ID == n.ID {
			return v.Name, v.Parallelism
		}
	}
	label = strings.ReplaceAll(n.Description, "<br/>", " ")
	label = strings.ReplaceAll(label, "&lt;", "<")
	label = strings.ReplaceAll(label, "&gt;", ">")
	return label, n.Parallelism
}

// liveState pulls the freshest sentinel sample for the vertex.
func (d *DagView) liveState(vid string) (busy float64, level string) {
	if d.deps.Sentinel == nil {
		return -1, ""
	}
	h := d.deps.Sentinel.History(sentinel.Key{JID: d.jid, VID: vid})
	if len(h) == 0 {
		return -1, ""
	}
	last := h[len(h)-1]
	return last.BusyMs, last.Level
}

func levelStyle(level string) func(string) string {
	switch strings.ToLower(level) {
	case "high":
		return func(s string) string { return styles.ErrText().Render(s) }
	case "low":
		return func(s string) string { return styles.WarnText().Render(s) }
	case "ok":
		return func(s string) string { return styles.OKText().Render(s) }
	}
	return func(s string) string { return styles.Dim().Render(s) }
}

func (d *DagView) render() string {
	nodes := topoOrder(d.plan.Nodes)
	num := map[string]int{}
	for i, n := range nodes {
		num[n.ID] = i + 1
	}
	numToID := map[int]string{}
	for id, n := range num {
		numToID[n] = id
	}
	// outputs[src] = sorted downstream node numbers
	outputs := map[string][]int{}
	for _, n := range nodes {
		for _, in := range n.Inputs {
			outputs[in.ID] = append(outputs[in.ID], num[n.ID])
		}
	}
	var b strings.Builder
	for _, n := range nodes {
		label, par := d.nodeLabel(n)
		busy, level := d.liveState(n.ID)
		glyph := "●"
		paint := levelStyle(level)
		busyText := "busy -"
		if busy >= 0 {
			busyText = fmt.Sprintf("busy %d%%", int(busy/10))
		}
		lvl := level
		if lvl == "" {
			lvl = "…"
		}
		line := fmt.Sprintf(" #%d %s %-52s p=%-3d %-9s %s",
			num[n.ID], glyph, truncate(label, 52), par, busyText, lvl)
		b.WriteString(paint(line) + "\n")
		outs := outputs[n.ID]
		sort.Ints(outs)
		for i, o := range outs {
			conn := " │  └─▶ #%d"
			if i < len(outs)-1 {
				conn = " │  ├─▶ #%d"
			}
			line := fmt.Sprintf(conn, o)
			// label the edge with the downstream vertex's inbound shuffle rate
			if er, ok := d.rates[numToID[o]]; ok && er.recs >= 0 {
				line += fmt.Sprintf("  %s rec/s · %s/s", fmtRate(er.recs), model.FmtBytes(int64(er.bytes)))
			}
			b.WriteString(styles.Dim().Render(line) + "\n")
		}
	}
	if len(nodes) == 0 {
		b.WriteString(styles.Dim().Render(" no plan nodes"))
	}
	b.WriteString("\n" + styles.Dim().Render(" ● node color = backpressure level · busy = busyTimeMsPerSecond/10"))
	return b.String()
}

// fmtRate renders a per-second rate compactly (12.3k / 431 / 1.2M).
func fmtRate(v float64) string {
	switch {
	case v >= 1e6:
		return fmt.Sprintf("%.1fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.1fk", v/1e3)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

// truncate hard-caps s at n runes with an ellipsis.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (d *DagView) View(width, height int) string {
	return framed(width, height, "Execution graph — "+d.name, -1, "", func(iw, ih int) string {
		out := ""
		used := 0
		if d.err != "" {
			out += styles.ErrText().Render("⚠ "+d.err) + "\n"
			used++
		}
		if !d.ready {
			d.vp = viewport.New(iw, ih-used)
			d.ready = true
		}
		d.vp.Width = iw
		d.vp.Height = ih - used
		d.vp.SetContent(d.render())
		return out + d.vp.View()
	})
}
