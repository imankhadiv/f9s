package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/imankhadiv/f9s/internal/analyze"
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
	deps  Deps
	jid   string
	name  string
	plan  model.Plan
	job   model.Job
	rates map[string]edgeRate
	stats map[string]dagStat
	xOff  int // horizontal scroll offset for the boxed layout
	vp    viewport.Model
	ready bool
	err   string
}

// NewDagView builds the DAG view for one job.
func NewDagView(deps Deps, jid, name string) *DagView {
	return &DagView{deps: deps, jid: jid, name: name, stats: map[string]dagStat{}}
}

func (d *DagView) Title() string   { return "dag(" + d.name + ")" }
func (d *DagView) Filtering() bool { return false }
func (d *DagView) Bindings() []key.Binding {
	return []key.Binding{
		binding("up", "↑↓", "scroll"),
		binding("left", "←→", "pan graph"),
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
		if d.ready {
			d.vp.SetContent(d.render(d.vp.Width))
		}
		return d, d.sampleCmds()
	case dagStatMsg:
		d.stats[msg.vid] = msg.stat
		if d.ready {
			d.vp.SetContent(d.render(d.vp.Width))
		}
		return d, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "left":
			if d.xOff > 0 {
				d.xOff -= 8
				if d.xOff < 0 {
					d.xOff = 0
				}
			}
			if d.ready {
				d.vp.SetContent(d.render(d.vp.Width))
			}
			return d, nil
		case "right":
			d.xOff += 8
			if d.ready {
				d.vp.SetContent(d.render(d.vp.Width))
			}
			return d, nil
		}
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
	hSource     boxHealth = iota // grey — source / idle / unsampled
	hHealthy                     // blue — steady
	hWarn                        // amber — busy/low backpressure
	hBottleneck                  // red — the slow stage
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
	case st.level == "high" || st.busyMax >= 80:
		return hBottleneck // the slow stage
	case st.level == "low" || st.busyMax >= 50:
		return hWarn
	case st.busyMax < 0 && st.level == "":
		return hSource // unsampled
	default:
		return hHealthy
	}
}

func boxColor(h boxHealth) lipgloss.TerminalColor {
	switch h {
	case hBottleneck:
		return styles.RedColor()
	case hWarn:
		return styles.AmberColor()
	case hHealthy:
		return styles.BlueColor()
	default:
		return styles.DimColor()
	}
}

// box renders one operator as a color-bordered tile with its live metrics.
func (d *DagView) box(n model.PlanNode, bw int) string {
	label, par := d.nodeLabel(n)
	st := d.stats[n.ID]
	col := boxColor(d.classify(n, st))
	tw := bw - 2 // text area inside the padding

	titleStyle := lipgloss.NewStyle().Foreground(col).Bold(true)
	bp := "Backpressured (max): —"
	if st.bpMax >= 0 {
		bp = fmt.Sprintf("Backpressured (max): %d%%", st.bpMax)
	} else if st.level != "" {
		bp = "Backpressured: " + strings.ToUpper(st.level)
	}
	bz := "Busy (max): —"
	if st.busyMax >= 0 {
		bz = fmt.Sprintf("Busy (max): %d%%", st.busyMax)
	}
	sk := "Data Skew: —"
	if st.skew >= 0 {
		sk = fmt.Sprintf("Data Skew: %d%%", st.skew)
	}
	body := titleStyle.Render(truncate(label, tw)) + "\n" +
		titleStyle.Render(truncate(fmt.Sprintf("Parallelism: %d", par), tw)) + "\n" +
		styles.Dim().Render(truncate(bp, tw)) + "\n" +
		styles.Dim().Render(truncate(bz, tw)) + "\n" +
		styles.Dim().Render(truncate(sk, tw))
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(col).
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

	var seg []string
	for ci, colNodes := range cols {
		var boxes []string
		for _, n := range colNodes {
			boxes = append(boxes, d.box(n, boxInner))
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
		hint = "\n" + styles.Dim().Render(fmt.Sprintf(" ← → pan · showing cols %d–%d of %d", d.xOff, min(d.xOff+iw, full), full))
	} else {
		d.xOff = 0
	}
	legend := "\n" + styles.Dim().Render(" ") +
		styles.Dim().Render("source") + " · " + styles.BlueText().Render("healthy") + " · " +
		styles.WarnText().Render("busy") + " · " + styles.ErrText().Render("bottleneck")
	return row + hint + "\n" + legend
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
