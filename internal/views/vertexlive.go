package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/metricts"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

// Tabs of the live vertex inspector. Everything that used to be a separate
// per-vertex view lives here, so "inspect this vertex" is one destination.
const (
	tabMetrics = iota
	tabSubtasks
	tabSkew
	tabBuffers
	tabRows
	tabCount
)

var tabNames = [tabCount]string{"metrics", "subtasks", "skew", "bp edge", "rows"}

const (
	// rowsKept is how many stdout lines per TaskManager the rows tab holds.
	rowsKept = 200
	// rowsPoll throttles stdout reads: the endpoint returns the whole file,
	// so it is only polled while the rows tab is open.
	rowsPoll = 3 * time.Second
)

// vertexStats is one sample of everything the metrics tab shows.
type vertexStats struct {
	inRecs, inBytes   float64
	outRecs, outBytes float64
	busy              float64 // ms/s, -1 unknown
	level             string  // backpressure level
	bpMax             int     // worst subtask backpressure %, -1 unknown
	subRecords        []float64
	inPool, outPool   []float64
	wmLag             int64 // ms behind wall clock
	wmKnown           bool
}

type vertexLiveMsg struct {
	stats vertexStats
	tms   []string
	err   error
}

// rowLine is one stdout line with the TaskManager that printed it.
type rowLine struct {
	tm   string
	text string
}

type vertexRowsMsg struct {
	rows        []rowLine
	perSec      float64 // -1 unknown
	unavailable bool    // the cluster does not keep stdout in a file
	err         error
}

// VertexLive is the live inspector for one vertex: a metrics tab fed by the
// REST API and the metric ring, and a rows tab that tails the stdout of the
// TaskManagers running THIS vertex (all a running job will show of its data).
type VertexLive struct {
	deps     Deps
	jid, job string
	vid      string
	name     string
	par      int

	tab      int
	subtasks *Subtasks      // tabSubtasks, built on first use
	skew     *SkewDetail    // tabSkew
	bufs     *BufferCompare // tabBuffers
	isSource bool
	stats    vertexStats
	tms      []string
	rows     []rowLine
	noStdout bool
	perSec   float64
	prevN    int
	polledA  time.Time
	vp       viewport.Model
	ready    bool
	loaded   bool
	err      string
}

// NewVertexLive builds the inspector for one vertex of a job.
func NewVertexLive(deps Deps, jid, job, vid, name string, parallelism int) *VertexLive {
	return &VertexLive{deps: deps, jid: jid, job: job, vid: vid, name: name, par: parallelism, perSec: -1,
		isSource: strings.HasPrefix(name, "Source")}
}

// NewVertexLiveTab opens the inspector straight on one tab — what the old
// per-vertex keys (t, d, e) now do.
func NewVertexLiveTab(deps Deps, jid, job, vid, name string, parallelism, tab int) *VertexLive {
	v := NewVertexLive(deps, jid, job, vid, name, parallelism)
	v.tab = tab
	return v
}

// sub returns the sub-view backing the active tab, building it on first use;
// cmd is its Init (nil when it already existed or the tab has no sub-view).
func (v *VertexLive) sub() (Model, tea.Cmd) {
	switch v.tab {
	case tabSubtasks:
		if v.subtasks == nil {
			v.subtasks = NewSubtasks(v.deps, v.jid, v.vid, v.name)
			return v.subtasks, v.subtasks.Init()
		}
		return v.subtasks, nil
	case tabSkew:
		if v.skew == nil {
			v.skew = NewSkewDetail(v.deps, v.jid, v.vid, v.name, v.par, v.isSource)
			return v.skew, v.skew.Init()
		}
		return v.skew, nil
	case tabBuffers:
		if v.bufs == nil {
			v.bufs = NewBufferCompare(v.deps, v.jid, v.vid, v.name, v.par)
			return v.bufs, v.bufs.Init()
		}
		return v.bufs, nil
	}
	return nil, nil
}

// subViews lists the sub-views built so far, so their own messages reach
// them whichever tab is on screen.
func (v *VertexLive) subViews() []Model {
	var out []Model
	if v.subtasks != nil {
		out = append(out, v.subtasks)
	}
	if v.skew != nil {
		out = append(out, v.skew)
	}
	if v.bufs != nil {
		out = append(out, v.bufs)
	}
	return out
}

func (v *VertexLive) Title() string   { return "live(" + truncate(v.name, 24) + ")" }
func (v *VertexLive) Filtering() bool { return false }
func (v *VertexLive) Bindings() []key.Binding {
	return []key.Binding{
		binding("tab", "tab", "metrics · subtasks · skew · bp edge · rows"),
		binding("up", "↑↓/pgup", "scroll"),
		binding("esc", "esc", "back"),
	}
}

func (v *VertexLive) Init() tea.Cmd {
	cmds := []tea.Cmd{v.Refresh()}
	if _, cmd := v.sub(); cmd != nil { // opened straight on a sub-view tab
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// Refresh samples the vertex; stdout is only read while the rows tab is open
// and at most every rowsPoll (the endpoint returns the whole file).
func (v *VertexLive) Refresh() tea.Cmd {
	cmds := []tea.Cmd{v.sample()}
	if s, _ := v.sub(); s != nil {
		cmds = append(cmds, s.Refresh())
	}
	if v.tab == tabRows && time.Since(v.polledA) >= rowsPoll {
		v.polledA = time.Now()
		cmds = append(cmds, v.tailRows())
	}
	return tea.Batch(cmds...)
}

// sample reads the vertex's live metrics and the TaskManagers running it.
func (v *VertexLive) sample() tea.Cmd {
	cl, jid, vid, par := v.deps.CL, v.jid, v.vid, v.par
	return func() tea.Msg {
		st := vertexStats{busy: -1, bpMax: -1}
		st.inRecs, st.inBytes, _ = cl.VertexInRates(jid, vid)
		st.outRecs, st.outBytes, _ = cl.VertexOutRates(jid, vid)
		if b, err := cl.VertexBusy(jid, vid); err == nil {
			st.busy = b
		}
		if bp, err := cl.Backpressure(jid, vid); err == nil {
			st.level, st.bpMax = bp.Level(), bp.MaxRatioPercent()
		}
		if sp, err := cl.VertexSubtaskPressure(jid, vid, par); err == nil {
			st.inPool, st.outPool = sp.InPool, sp.OutPool
		}
		if recs, err := cl.VertexSubtaskRecords(jid, vid, par); err == nil {
			st.subRecords = recs
		}
		if wm, err := cl.VertexWatermarkDetail(jid, vid); err == nil {
			newest := int64(0)
			for _, w := range wm.Subtasks {
				if w > newest {
					newest = w
				}
			}
			if newest > 0 {
				st.wmLag, st.wmKnown = time.Now().UnixMilli()-newest, true
			}
		}
		msg := vertexLiveMsg{stats: st}
		if subs, err := cl.VertexSubtasks(jid, vid); err == nil {
			msg.tms = taskManagersOf(subs)
		}
		return msg
	}
}

// taskManagersOf lists the distinct TaskManagers running a vertex's subtasks.
func taskManagersOf(subs []model.SubtaskInfo) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range subs {
		if s.TaskManagerID == "" || seen[s.TaskManagerID] {
			continue
		}
		seen[s.TaskManagerID] = true
		out = append(out, s.TaskManagerID)
	}
	sort.Strings(out)
	return out
}

// tailRows reads the stdout of every TaskManager running this vertex.
func (v *VertexLive) tailRows() tea.Cmd {
	cl, tms, prev, since := v.deps.CL, append([]string(nil), v.tms...), v.prevN, time.Since(v.polledA)
	if since <= 0 {
		since = rowsPoll
	}
	return func() tea.Msg {
		var rows []rowLine
		noFile := len(tms) > 0
		for _, tm := range tms {
			text, err := cl.TMStdout(tm)
			if err != nil {
				continue
			}
			if stdoutUnavailable(text) {
				continue // Flink answers 200 with an explanation, not output
			}
			noFile = false
			rows = append(rows, tailRows(tm, text)...)
		}
		return vertexRowsMsg{rows: rows, unavailable: noFile, perSec: rowsPerSec(prev, len(rows), since)}
	}
}

// stdoutUnavailable reports whether Flink answered the stdout request with an
// explanation instead of output. The endpoint returns 200 with a message body
// when the TaskExecutor keeps no stdout FILE — the normal case under Docker
// and Kubernetes, where stdout goes to the container log — and printing that
// message as if it were job output would be a lie.
func stdoutUnavailable(text string) bool {
	t := strings.ToLower(text)
	return strings.Contains(t, "does not exist on the taskexecutor") ||
		strings.Contains(t, "please use \"kubectl logs")
}

// tailRows keeps the newest rowsKept non-empty lines of one TM's stdout.
func tailRows(tm, text string) []rowLine {
	lines := strings.Split(text, "\n")
	out := make([]rowLine, 0, rowsKept)
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, rowLine{tm: tm, text: l})
	}
	if len(out) > rowsKept {
		out = out[len(out)-rowsKept:]
	}
	return out
}

// rowsPerSec estimates the print rate between two polls. The first poll and a
// shrinking file (rotated, or the job restarted) yield -1: unknown beats a
// number that is wrong.
func rowsPerSec(prev, next int, elapsed time.Duration) float64 {
	if prev <= 0 || next < prev || elapsed <= 0 {
		return -1
	}
	return float64(next-prev) / elapsed.Seconds()
}

func (v *VertexLive) Update(msg tea.Msg) (Model, tea.Cmd) {
	// a sub-view's own messages must reach it whichever tab is on screen
	switch msg.(type) {
	case vertexLiveMsg, vertexRowsMsg, tea.KeyMsg:
	default:
		for _, s := range v.subViews() {
			m, cmd := s.Update(msg)
			v.replaceSub(m)
			if cmd != nil {
				return v, cmd
			}
		}
		return v, nil
	}
	switch msg := msg.(type) {
	case vertexLiveMsg:
		v.loaded = true
		if msg.err != nil {
			v.err = msg.err.Error()
			return v, nil
		}
		v.err = ""
		v.stats = msg.stats
		if msg.tms != nil {
			v.tms = msg.tms
		}
		return v, nil
	case vertexRowsMsg:
		if msg.err != nil {
			v.err = msg.err.Error()
			return v, nil
		}
		v.rows, v.noStdout, v.perSec, v.prevN = msg.rows, msg.unavailable, msg.perSec, len(msg.rows)
		if v.ready {
			v.vp.SetContent(v.rowsBody(v.vp.Width))
			v.vp.GotoBottom()
		}
		return v, nil
	case tea.KeyMsg:
		if msg.String() == "tab" || msg.String() == "shift+tab" {
			step := 1
			if msg.String() == "shift+tab" {
				step = tabCount - 1
			}
			v.tab = (v.tab + step) % tabCount
			if v.tab == tabRows {
				v.polledA = time.Now()
				return v, v.tailRows()
			}
			_, cmd := v.sub()
			return v, cmd
		}
		if s, _ := v.sub(); s != nil { // the sub-view owns the keys on its tab
			m, cmd := s.Update(msg)
			v.replaceSub(m)
			return v, cmd
		}
		if v.tab == tabRows && v.ready {
			var cmd tea.Cmd
			v.vp, cmd = v.vp.Update(msg)
			return v, cmd
		}
	}
	return v, nil
}

// replaceSub stores back the sub-view returned by its own Update.
func (v *VertexLive) replaceSub(m Model) {
	switch t := m.(type) {
	case *Subtasks:
		v.subtasks = t
	case *SkewDetail:
		v.skew = t
	case *BufferCompare:
		v.bufs = t
	}
}

// ring returns the vertex's sampled history for a metric.
func (v *VertexLive) ring(metric string) []float64 {
	return v.deps.Metrics.Values(metricts.Key{Scope: metricts.ScopeVertex, ID: metricts.VertexID(v.jid, v.vid), Metric: metric})
}

func pctOf(msPerSec float64) string {
	if msPerSec < 0 {
		return "-"
	}
	return fmt.Sprintf("%d%%", int(msPerSec/10))
}

// metricsBody renders the metrics tab.
func (v *VertexLive) metricsBody(width int) string {
	st := v.stats
	var b strings.Builder
	line := func(label, value string) {
		b.WriteString(" " + styles.Dim().Render(fmt.Sprintf("%-14s", label)) + value + "\n")
	}
	line("in", fmt.Sprintf("%s rec/s   %s/s", fmtRate(st.inRecs), model.FmtBytes(int64(st.inBytes))))
	line("out", fmt.Sprintf("%s rec/s   %s/s", fmtRate(st.outRecs), model.FmtBytes(int64(st.outBytes))))

	busy := styles.Severity(st.busy/1000, 0.5, 0.8).Render(pctOf(st.busy))
	bp := "-"
	if st.bpMax >= 0 {
		bp = styles.Severity(float64(st.bpMax)/100, 0.3, 0.6).Render(fmt.Sprintf("%d%%", st.bpMax))
	}
	lvl := ""
	if st.level != "" {
		lvl = styles.Dim().Render("  level " + st.level)
	}
	line("busy", busy)
	line("backpressure", bp+lvl)

	if st.wmKnown {
		line("watermark lag", model.FmtDuration(st.wmLag))
	} else {
		line("watermark lag", styles.Dim().Render("no watermark on this vertex"))
	}
	if n := len(st.inPool); n > 0 {
		line("buffers", fmt.Sprintf("in %.2f · out %.2f %s", maxOf(st.inPool), maxOf(st.outPool),
			styles.Dim().Render("(worst subtask; in≈1.0 = this vertex is the one being blocked)")))
	}

	if len(st.subRecords) > 0 {
		skew := analyze.SkewScore(st.subRecords)
		b.WriteString("\n " + styles.ViewTitle().Render("subtasks") +
			styles.Dim().Render(fmt.Sprintf("  records/s per subtask · skew %d%%", skew)) + "\n")
		maxV := maxOf(st.subRecords)
		barW := min(40, max(10, width-34))
		for i, val := range st.subRecords {
			if i >= 12 {
				b.WriteString(styles.Dim().Render(fmt.Sprintf("  … and %d more subtasks", len(st.subRecords)-i)) + "\n")
				break
			}
			b.WriteString(fmt.Sprintf("  #%-3d %s %10s\n", i,
				styles.Dim().Render(fmt.Sprintf("%-*s", barW, bar(val, maxV, barW))), fmtRate(val)))
		}
	}

	if hist := v.ring(metricts.Busy); len(hist) >= 2 {
		b.WriteString("\n " + styles.Dim().Render("busy 5m  ") + styles.OKText().Render(styles.Spark(tailN(hist, 40))) + "\n")
	}
	if hist := v.ring(metricts.RecordsIn); len(hist) >= 2 {
		b.WriteString(" " + styles.Dim().Render("in/s 5m  ") + styles.OKText().Render(styles.Spark(tailN(hist, 40))) + "\n")
	}
	return b.String()
}

func maxOf(vals []float64) float64 {
	m := 0.0
	for _, v := range vals {
		if v > m {
			m = v
		}
	}
	return m
}

// rowsBody renders the rows tab: what this vertex's TaskManagers printed.
func (v *VertexLive) rowsBody(width int) string {
	if v.noStdout {
		return styles.Dim().Render(
			" This cluster keeps no stdout file for its TaskManagers.\n\n" +
				" Flink only serves stdout when the TaskExecutor writes it to a file; under Docker\n" +
				" and Kubernetes it goes to the container log instead, so there is nothing to tail\n" +
				" here. Read it with `docker logs <container>` or `kubectl logs <pod>`.\n\n" +
				" The metrics tab still shows this vertex's live rates, and <L> tails the job's\n" +
				" merged TaskManager logs.")
	}
	if len(v.rows) == 0 {
		return styles.Dim().Render(
			" This vertex prints nothing to stdout.\n\n" +
				" A running Flink job does not expose the records flowing through it: f9s can only\n" +
				" show what the job itself prints, which in practice means a print/console sink.\n\n" +
				" For a source or a mid-graph operator there is nothing to tail — use the metrics\n" +
				" tab for its live rates, <L> for the job's merged TaskManager logs, or <t> for the\n" +
				" per-subtask table.")
	}
	var b strings.Builder
	for _, r := range v.rows {
		b.WriteString(styles.Dim().Render(fmt.Sprintf("%-22s ", truncate(r.tm, 21))) + truncate(r.text, max(10, width-24)) + "\n")
	}
	return b.String()
}

// AIContext hands the AI panel the vertex's live picture.
func (v *VertexLive) AIContext() (string, []string) {
	st := v.stats
	var b strings.Builder
	fmt.Fprintf(&b, "## On screen: live vertex %q of job %s (parallelism %d)\n", v.name, v.job, v.par)
	fmt.Fprintf(&b, "in %s rec/s · out %s rec/s · busy %s · backpressure %s (level %s)\n",
		fmtRate(st.inRecs), fmtRate(st.outRecs), pctOf(st.busy), fmt.Sprint(st.bpMax)+"%", orDash(st.level))
	if st.wmKnown {
		fmt.Fprintf(&b, "watermark lag %s\n", model.FmtDuration(st.wmLag))
	}
	if len(st.subRecords) > 0 {
		fmt.Fprintf(&b, "per-subtask records/s skew %d%%\n", analyze.SkewScore(st.subRecords))
	}
	if len(v.rows) > 0 {
		fmt.Fprintf(&b, "stdout (last %d lines, %d TaskManagers):\n", min(5, len(v.rows)), len(v.tms))
		for _, r := range v.rows[max(0, len(v.rows)-5):] {
			fmt.Fprintf(&b, "  %s\n", truncate(r.text, 120))
		}
	}
	return b.String(), []string{v.job}
}

func (v *VertexLive) View(width, height int) string {
	head := " "
	for i, t := range tabNames {
		if i == v.tab {
			head += styles.Header().Render("["+t+"]") + " "
		} else {
			head += styles.Dim().Render(" "+t+" ") + " "
		}
	}
	right := v.job
	if v.tab == tabRows {
		right = fmt.Sprintf("%d TaskManagers · ", len(v.tms))
		if v.perSec >= 0 {
			right += fmt.Sprintf("%.1f rows/s", v.perSec)
		} else {
			right += "rows/s …"
		}
	}
	return framed(width, height, "Live · "+v.name, -1, right, func(iw, ih int) string {
		out := head + styles.Dim().Render(" <tab> switches") + "\n"
		used := 1
		if v.err != "" {
			out += errorBanner(v.err)
			used++
		}
		if s, _ := v.sub(); s != nil {
			if sub, ok := s.(*Subtasks); ok {
				return out + sub.body(iw, max(1, ih-used))
			}
			return out + s.View(iw, max(1, ih-used))
		}
		if v.tab == tabMetrics {
			if !v.loaded {
				return out + emptyState(" sampling…")
			}
			return out + v.metricsBody(iw)
		}
		if !v.ready {
			v.vp = viewport.New(iw, max(1, ih-used))
			v.ready = true
		}
		v.vp.Width, v.vp.Height = iw, max(1, ih-used)
		v.vp.SetContent(v.rowsBody(iw))
		return out + v.vp.View()
	})
}
