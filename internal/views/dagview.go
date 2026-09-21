package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/kube"
	"github.com/imankhadiv/f9s/internal/metricts"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/sentinel"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type edgeRate struct {
	recs, bytes float64
}

type dagMsg struct {
	plan    model.Plan
	job     model.Job
	rates   map[string]edgeRate // downstream vertex id → inbound shuffle rate
	targets map[string]int      // vertex id → target (upper-bound) parallelism
	err     error
}

// dagStat is the per-vertex live snapshot shown inside a box.
type dagStat struct {
	level   string // backpressure level: ok/low/high
	bpMax   int    // backpressured (max) %, -1 unknown
	busyMax int    // busy (max) %, -1 unknown
	skew    int    // data skew %, -1 unknown
}

type dagStatMsg struct {
	vid  string
	stat dagStat
}

// DagView paints the job's execution graph as an ASCII DAG with live
// busy/backpressure state on every node — topology the Web UI shows only
// as an SVG you can't grep.
type DagView struct {
	deps    Deps
	jid     string
	name    string
	plan    model.Plan
	job     model.Job
	rates   map[string]edgeRate
	stats   map[string]dagStat
	targets map[string]int // vertex id → target parallelism (pending rescale)
	xOff    int            // horizontal scroll offset for the boxed layout
	selVID  string         // selected vertex id (by id, so refreshes don't move it)
	order   []string       // box layout order (column-major), for ←→ selection
	vp      viewport.Model
	ready   bool
	err     string

	auto       dagAuto // operator autoscaler state for this job (via kubectl)
	autoAt     time.Time
	loadKube   func(string) ([]kube.FlinkDeployment, error)
	loadEvents func(kubeContext, ns, name string) ([]kube.Event, error)
	now        func() time.Time
}

// dagAuto links the running job to its FlinkDeployment and the operator
// autoscaler's latest ScalingReport.
type dagAuto struct {
	linked   bool
	ns, name string
	mode     string // on / advisory / off
	target   float64
	boundary float64
	recs     map[string]analyze.ScalingChange // vertex id → latest decision
	reportAt time.Time
}

type dagAutoMsg struct{ auto dagAuto }

// autoRefreshEvery throttles the kubectl lookups behind the autoscaler overlay.
const autoRefreshEvery = 30 * time.Second

// NewDagView builds the DAG view for one job.
func NewDagView(deps Deps, jid, name string) *DagView {
	return &DagView{deps: deps, jid: jid, name: name, stats: map[string]dagStat{},
		auto:     dagAuto{target: analyze.DefaultTargetUtilization, boundary: analyze.DefaultUtilizationBound},
		loadKube: kube.Deployments, now: time.Now,
		loadEvents: func(kc, ns, name string) ([]kube.Event, error) { return kube.Events(kc, ns, "FlinkDeployment", name) }}
}

func (d *DagView) Title() string   { return "dag(" + d.name + ")" }
func (d *DagView) Filtering() bool { return false }
func (d *DagView) Bindings() []key.Binding {
	return []key.Binding{
		binding("left", "←→", "select box"),
		binding("R", "R", "rescale"),
		binding("O", "O", "pin parallelism (operator CR)"),
		binding("up", "↑↓", "scroll"),
		binding("shift+left", "shift+←→", "pan"),
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
		targets, _ := cl.ResourceTargets(jid) // best-effort; nil when unsupported
		return dagMsg{plan: plan, job: job, rates: rates, targets: targets, err: nil}
	}
}

// autoCmd looks up the job's FlinkDeployment (joined on status.jobStatus.jobId)
// and its newest ScalingReport event. Only runs when the context maps a
// kubeContext; any kubectl failure just leaves the overlay off.
func (d *DagView) autoCmd() tea.Cmd {
	kc, err := configuredKubeContext(d.deps)
	if err != nil || d.now().Sub(d.autoAt) < autoRefreshEvery {
		return nil
	}
	d.autoAt = d.now()
	jid, loadKube, loadEvents := d.jid, d.loadKube, d.loadEvents
	return func() tea.Msg {
		a := dagAuto{target: analyze.DefaultTargetUtilization, boundary: analyze.DefaultUtilizationBound}
		ds, err := loadKube(kc)
		if err != nil {
			return dagAutoMsg{auto: a}
		}
		for _, dep := range ds {
			if dep.JobID == "" || dep.JobID != jid {
				continue
			}
			a.linked, a.ns, a.name = true, dep.Namespace, dep.Name
			a.mode = AutoscalerMode(dep.FlinkConfig)
			a.target = analyze.ConfigFloat(dep.FlinkConfig, "target.utilization", analyze.DefaultTargetUtilization)
			a.boundary = analyze.ConfigFloat(dep.FlinkConfig, "target.utilization.boundary", analyze.DefaultUtilizationBound)
			if es, err := loadEvents(kc, dep.Namespace, dep.Name); err == nil {
				for _, e := range es { // newest first
					if e.Reason != "ScalingReport" {
						continue
					}
					if cs := analyze.ParseScalingReport(e.Message); len(cs) > 0 {
						a.recs = map[string]analyze.ScalingChange{}
						for _, c := range cs {
							a.recs[c.VID] = c
						}
						a.reportAt = e.Last
						break
					}
				}
			}
			break
		}
		return dagAutoMsg{auto: a}
	}
}

// busyAvg is the vertex's mean busy fraction over the last 5 minutes of
// sampled history (ok=false with fewer than 3 samples).
func (d *DagView) busyAvg(vid string) (float64, bool) {
	pts := d.deps.Metrics.Series(metricts.Key{Scope: metricts.ScopeVertex, ID: metricts.VertexID(d.jid, vid), Metric: metricts.Busy})
	pts = metricts.Since(pts, d.now().Add(-5*time.Minute))
	if len(pts) < 3 {
		return 0, false
	}
	var sum float64
	for _, p := range pts {
		sum += p.Value
	}
	return sum / float64(len(pts)) / 1000, true
}

// recommendation returns the parallelism to show next to the current one:
// the operator's latest ScalingReport decision if there is one, otherwise
// f9s's own utilization estimate. Down-scale estimates are shown only for
// autoscaled jobs; elsewhere an idle vertex is not a problem worth flagging.
func (d *DagView) recommendation(vid string, par int) (to int, source string) {
	if c, ok := d.auto.recs[strings.ToLower(vid)]; ok && c.To > 0 && c.To != par {
		return c.To, "operator"
	}
	busy, ok := d.busyAvg(vid)
	if !ok {
		return par, ""
	}
	est := analyze.EstimateParallelism(par, busy, d.auto.target, d.auto.boundary, 0)
	autoscaled := d.auto.linked && d.auto.mode != "off"
	if est == par || (est < par && !autoscaled) {
		return par, ""
	}
	return est, "estimate"
}

// sampleCmds fetches per-vertex backpressure, busy-max and skew in parallel so
// each box shows the same live metrics as the Flink Web UI overview graph.
func (d *DagView) sampleCmds() tea.Cmd {
	cl := d.deps.CL
	jid := d.jid
	cmds := make([]tea.Cmd, 0, len(d.job.Vertices))
	for _, v := range d.job.Vertices {
		vid, par := v.ID, v.Parallelism
		cmds = append(cmds, func() tea.Msg {
			st := dagStat{bpMax: -1, busyMax: -1, skew: -1}
			if bp, err := cl.Backpressure(jid, vid); err == nil {
				st.level = bp.Level()
				st.bpMax = bp.MaxRatioPercent()
			}
			// busy (max across subtasks), falling back to the average
			if bs, err := cl.VertexSubtaskBusy(jid, vid, par); err == nil && len(bs) > 0 {
				m := 0.0
				for _, b := range bs {
					if b > m {
						m = b
					}
				}
				st.busyMax = int(m / 10)
			} else if avg, err := cl.VertexBusy(jid, vid); err == nil && avg >= 0 {
				st.busyMax = int(avg / 10)
			}
			if recs, err := cl.VertexSubtaskRecords(jid, vid, par); err == nil {
				st.skew = analyze.SkewScore(recs)
			}
			return dagStatMsg{vid: vid, stat: st}
		})
	}
	return tea.Batch(cmds...)
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
		d.targets = msg.targets
		if _, ok := d.nodeByID(d.selVID); !ok && len(d.plan.Nodes) > 0 {
			d.selVID = topoOrder(d.plan.Nodes)[0].ID // default/repair selection
		}
		if d.ready {
			d.vp.SetContent(d.render(d.vp.Width))
		}
		return d, tea.Batch(d.sampleCmds(), d.autoCmd())
	case dagAutoMsg:
		d.auto = msg.auto
		if d.ready {
			d.vp.SetContent(d.render(d.vp.Width))
		}
		return d, nil
	case dagStatMsg:
		d.stats[msg.vid] = msg.stat
		if d.ready {
			d.vp.SetContent(d.render(d.vp.Width))
		}
		return d, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "left", "right", "tab", "shift+tab":
			delta := 1
			if msg.String() == "left" || msg.String() == "shift+tab" {
				delta = -1
			}
			d.moveSel(delta)
			d.autoPan()
			if d.ready {
				d.vp.SetContent(d.render(d.vp.Width))
			}
			return d, nil
		case "shift+left", "shift+right":
			if msg.String() == "shift+left" {
				d.xOff -= 8
			} else {
				d.xOff += 8
			}
			if d.xOff < 0 {
				d.xOff = 0
			}
			if d.ready {
				d.vp.SetContent(d.render(d.vp.Width))
			}
			return d, nil
		case "O":
			// durable per-vertex pin in the FlinkDeployment — only when the
			// job is linked to its CR, prefilled with the recommendation
			if !d.auto.linked {
				return d, flashErrV("no FlinkDeployment linked to this job (needs kubeContext and an operator-managed job)")
			}
			n, ok := d.nodeByID(d.selVID)
			if !ok {
				return d, nil
			}
			_, par := d.nodeLabel(n)
			def := par
			if to, src := d.recommendation(n.ID, par); src != "" {
				def = to
			}
			args := map[string]string{"namespace": d.auto.ns, "name": d.auto.name, "vertex": n.ID}
			return d, propose(d.deps, "set_parallelism_override", d.auto.ns+"/"+d.auto.name, args,
				&ProposalInput{Arg: "parallelism", Label: "pin parallelism (0 removes the pin)", Default: fmt.Sprint(def)})
		case "R":
			if g := readOnlyGuard(d.deps); g != nil {
				return d, g
			}
			n, ok := d.nodeByID(d.selVID)
			if !ok {
				return d, nil
			}
			label, par := d.nodeLabel(n)
			jid, jobName, vid := d.jid, d.name, n.ID
			return d, func() tea.Msg {
				return RescaleMsg{JID: jid, Job: jobName, VID: vid, Vertex: label, Current: par}
			}
		}
		if d.ready {
			var cmd tea.Cmd
			d.vp, cmd = d.vp.Update(msg)
			return d, cmd
		}
	}
	return d, nil
}

// moveSel shifts the selection along the column-major box order.
func (d *DagView) moveSel(delta int) {
	if len(d.order) == 0 {
		return
	}
	i := 0
	for k, id := range d.order {
		if id == d.selVID {
			i = k
			break
		}
	}
	i += delta
	if i < 0 {
		i = 0
	}
	if i >= len(d.order) {
		i = len(d.order) - 1
	}
	d.selVID = d.order[i]
}

// autoPan scrolls horizontally so the selected box stays on screen.
func (d *DagView) autoPan() {
	const pitch, boxW = boxInner + 2 + 5, boxInner + 2 // column pitch, box width
	col := rank(d.plan.Nodes)[d.selVID]
	left, right := col*pitch, col*pitch+boxW
	iw := d.vp.Width
	if left < d.xOff {
		d.xOff = left
	} else if iw > 0 && right > d.xOff+iw {
		d.xOff = right - iw
	}
	if d.xOff < 0 {
		d.xOff = 0
	}
}

// AIContext gives the AI panel the graph topology with each operator's live
// health and any pending rescale — the same signals the boxes show.
func (d *DagView) AIContext() (string, []string) {
	var b strings.Builder
	fmt.Fprintf(&b, "## On screen: execution graph of %s\n", d.name)
	pct := func(v int) string {
		if v < 0 {
			return "?"
		}
		return fmt.Sprintf("%d%%", v)
	}
	for _, n := range topoOrder(d.plan.Nodes) {
		label, par := d.nodeLabel(n)
		st := d.stats[n.ID]
		fmt.Fprintf(&b, "- %s p=%d bp=%s busy=%s skew=%s", label, par, pct(st.bpMax), pct(st.busyMax), pct(st.skew))
		if t, ok := d.targets[n.ID]; ok && t > 0 && t != par {
			fmt.Fprintf(&b, " (rescale pending → %d)", t)
		} else if to, src := d.recommendation(n.ID, par); src != "" {
			fmt.Fprintf(&b, " (%s suggests parallelism %d)", map[string]string{"operator": "operator autoscaler", "estimate": "f9s utilization estimate"}[src], to)
		}
		b.WriteString("\n")
	}
	if d.auto.linked {
		fmt.Fprintf(&b, "FlinkDeployment %s/%s · autoscaler %s · target utilization %.0f%%\n", d.auto.ns, d.auto.name, d.auto.mode, d.auto.target*100)
	}
	return b.String(), []string{d.name}
}

// nodeByID finds a plan node by vertex id.
func (d *DagView) nodeByID(id string) (model.PlanNode, bool) {
	for _, n := range d.plan.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return model.PlanNode{}, false
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

// render dispatches on width: the Flink-style boxed pipeline when there's room
// for at least one box, otherwise the compact ASCII list (which is safe at any
// size, including the render-safety matrix's 1×1).
func (d *DagView) render(iw int) string {
	if len(d.plan.Nodes) == 0 {
		return styles.Dim().Render(" no plan nodes")
	}
	if iw < boxInner+4 {
		return d.renderList()
	}
	return d.renderBoxes(iw)
}

func (d *DagView) renderList() string {
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

// --- Flink-Web-UI-style boxed pipeline ---

const (
	boxInner = 26          // inner content width of a vertex box (incl. padding)
	boxBody  = 5           // title, parallelism, backpressured, busy, skew
	boxH     = boxBody + 2 // + rounded border top/bottom
)

type boxHealth int

const (
	hSource        boxHealth = iota // grey — source / idle / unsampled
	hFlow                           // blue — steady dataflow
	hWarn                           // orange — elevated busy/low backpressure
	hBusy                           // red — saturated downstream bottleneck
	hBackpressured                  // black/contrast — blocked by downstream work
)

// classify maps a vertex's live metrics to a health color, reusing the same
// busy/backpressure thresholds as the rest of the UI.
func (d *DagView) classify(n model.PlanNode, st dagStat) boxHealth {
	// Sources stay neutral (grey), as in the Flink Web UI: a source is often
	// backpressured as a *symptom* of a downstream bottleneck, not the cause.
	if len(n.Inputs) == 0 {
		return hSource
	}
	switch {
	case st.level == "high":
		return hBackpressured
	case st.busyMax >= 80:
		return hBusy // saturated: this vertex is the bottleneck
	case st.level == "low" || st.busyMax >= 50:
		return hWarn
	case st.busyMax < 0 && st.level == "":
		return hSource // unsampled
	default:
		return hFlow
	}
}

func boxColor(h boxHealth) lipgloss.TerminalColor {
	switch h {
	case hBackpressured:
		return styles.BackpressureColor()
	case hBusy:
		return styles.RedColor()
	case hWarn:
		return styles.AmberColor()
	case hFlow:
		return styles.FlowColor()
	default:
		return styles.DimColor()
	}
}

// gaugeLine renders "LABL ███░░ NN%" width-exact inside a box (gw+10 cells):
// label(4)+space + gw bar + space + "NNN%"(4). A negative pct is unsampled.
func gaugeLine(label string, pct, gw int, warnAt, critAt float64) string {
	lab := styles.Dim().Render(fmt.Sprintf("%-4s ", label))
	if pct < 0 {
		return lab + styles.Dim().Render(strings.Repeat("·", gw)+"   —")
	}
	bar := styles.Gauge(float64(pct)/100, gw, warnAt, critAt)
	return lab + bar + styles.Dim().Render(fmt.Sprintf(" %3d%%", pct))
}

// box renders one operator as a modern tile: a filled health-colored title
// bar, the parallelism, and three severity gauge bars (backpressure/busy/skew).
// A selected box gets a thick border, a ▸ marker, and a reversed title bar.
func (d *DagView) box(n model.PlanNode, bw int, selected bool) string {
	label, par := d.nodeLabel(n)
	st := d.stats[n.ID]
	col := boxColor(d.classify(n, st))
	tw := bw - 2 // text area inside the padding

	// title: bold health-colored text; the selected box gets a reverse-video
	// full-width header bar + ▸ marker (readable on any theme, unlike a dim
	// background fill).
	marker := ""
	titleStyle := lipgloss.NewStyle().Foreground(col).Bold(true)
	if selected {
		marker = "▸ "
		titleStyle = titleStyle.Reverse(true).Width(tw)
	}
	title := titleStyle.Render(truncate(marker+label, tw))
	// pending rescale: running parallelism differs from the scheduler target
	parLine := lipgloss.NewStyle().Foreground(col).Bold(true).Render(fmt.Sprintf("parallelism  %d", par))
	if t, ok := d.targets[n.ID]; ok && t > 0 && t != par {
		parLine = styles.StateText().Bold(true).Render(fmt.Sprintf("parallelism  %d → %d ⟳", par, t))
	} else if to, src := d.recommendation(n.ID, par); src == "operator" {
		badge := "●"
		if !d.auto.recs[strings.ToLower(n.ID)].Applied {
			badge = "◐"
		}
		parLine = styles.StateText().Bold(true).Render(fmt.Sprintf("parallelism  %d ⇒ %d %s", par, to, badge))
	} else if src == "estimate" {
		parLine = lipgloss.NewStyle().Foreground(col).Bold(true).Render(fmt.Sprintf("parallelism  %d", par)) +
			styles.Dim().Render(fmt.Sprintf(" ⇒ %d~", to))
	}

	body := title + "\n" +
		parLine + "\n" +
		gaugeLine("bp", st.bpMax, 14, 0.5, 0.8) + "\n" +
		gaugeLine("busy", st.busyMax, 14, 0.5, 0.8) + "\n" +
		gaugeLine("skew", st.skew, 14, 0.3, 0.6)

	border := lipgloss.RoundedBorder()
	bc := col
	if selected {
		border = lipgloss.ThickBorder() // ┏━┓ — obvious even without color
		bc = styles.AccentColor()
	}
	return lipgloss.NewStyle().
		Border(border).
		BorderForeground(bc).
		Padding(0, 1).
		Width(bw).
		Render(body)
}

// connector is the dashed arrow + edge label column between two box columns,
// boxH rows tall so it aligns to the box midline under JoinHorizontal(Top).
func connector(label string) string {
	arrow := styles.Dim().Render("╌╌▶")
	w := lipgloss.Width(arrow) + 2
	rows := make([]string, boxH)
	mid := boxH / 2
	for i := range rows {
		switch i {
		case mid:
			rows[i] = " " + arrow + " "
		case mid + 1:
			rows[i] = " " + styles.Dim().Render(truncate(label, 10))
		default:
			rows[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(rows, "\n")
}

// edgeLabel is the ship strategy Flink prints on a node's inbound edge.
func edgeLabel(n model.PlanNode) string {
	for _, in := range n.Inputs {
		if in.ShipStrategy != "" {
			return strings.ToUpper(in.ShipStrategy)
		}
	}
	return ""
}

// rank assigns each node its longest-path depth from a source (its column).
func rank(nodes []model.PlanNode) map[string]int {
	r := map[string]int{}
	for _, n := range topoOrder(nodes) {
		for _, in := range n.Inputs {
			if r[in.ID]+1 > r[n.ID] {
				r[n.ID] = r[in.ID] + 1
			}
		}
	}
	return r
}

func (d *DagView) renderBoxes(iw int) string {
	nodes := topoOrder(d.plan.Nodes)
	r := rank(nodes)
	maxR := 0
	for _, n := range nodes {
		if r[n.ID] > maxR {
			maxR = r[n.ID]
		}
	}
	cols := make([][]model.PlanNode, maxR+1)
	for _, n := range nodes {
		cols[r[n.ID]] = append(cols[r[n.ID]], n)
	}

	d.order = d.order[:0]
	var seg []string
	for ci, colNodes := range cols {
		var boxes []string
		for _, n := range colNodes {
			d.order = append(d.order, n.ID)
			boxes = append(boxes, d.box(n, boxInner, n.ID == d.selVID))
		}
		seg = append(seg, lipgloss.JoinVertical(lipgloss.Left, interleaveBlank(boxes)...))
		if ci < len(cols)-1 {
			lbl := ""
			for _, n := range cols[ci+1] {
				if e := edgeLabel(n); e != "" {
					lbl = e
					break
				}
			}
			seg = append(seg, connector(lbl))
		}
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, seg...)

	// horizontal pan: crop every line to the window [xOff, xOff+iw]
	full := lipgloss.Width(row)
	hint := ""
	if full > iw {
		if d.xOff > full-iw {
			d.xOff = full - iw
		}
		lines := strings.Split(row, "\n")
		for i, ln := range lines {
			lines[i] = ansi.Cut(ln, d.xOff, d.xOff+iw)
		}
		row = strings.Join(lines, "\n")
		hint = "\n" + styles.Dim().Render(fmt.Sprintf(" showing cols %d–%d of %d", d.xOff, min(d.xOff+iw, full), full))
	} else {
		d.xOff = 0
	}
	legend := "\n " +
		styles.Dim().Render("source / unsampled") + " · " + styles.FlowText().Render("steady") + " · " +
		styles.WarnText().Render("elevated") + " · " + styles.ErrText().Render("busy") + " · " +
		styles.BackpressureText().Render("backpressured") +
		styles.Dim().Render("   —   ←→ select · R rescale · shift+←→ pan")
	return row + hint + "\n" + legend + d.autoLegend()
}

// autoLegend explains the parallelism suggestions and, for a job managed by
// the operator, the autoscaler's state.
func (d *DagView) autoLegend() string {
	var parts []string
	if d.auto.linked {
		badge := map[string]string{"on": "● on", "advisory": "◐ advisory (recommends only)", "off": "○ off"}[d.auto.mode]
		line := fmt.Sprintf("autoscaler %s · %s/%s · target %.0f%% ±%.0f%%", badge, d.auto.ns, d.auto.name, d.auto.target*100, d.auto.boundary*100)
		if !d.auto.reportAt.IsZero() {
			line += " · last ScalingReport " + ago(d.auto.reportAt) + " ago"
		}
		parts = append(parts, line)
	}
	parts = append(parts, fmt.Sprintf("⇒ N● operator decision · ⇒ N◐ operator recommendation · ⇒ N~ f9s estimate (5m avg busy vs %.0f%% target)", d.auto.target*100))
	return "\n " + styles.Dim().Render(strings.Join(parts, "   "))
}

// interleaveBlank puts a blank spacer line between stacked boxes in a column.
func interleaveBlank(boxes []string) []string {
	if len(boxes) <= 1 {
		return boxes
	}
	out := make([]string, 0, len(boxes)*2-1)
	for i, b := range boxes {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, b)
	}
	return out
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
		d.vp.SetContent(d.render(iw))
		return out + d.vp.View()
	})
}
