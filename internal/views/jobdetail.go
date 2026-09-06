package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/imankhadiv/f9s/internal/analyze"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/snapshot"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type jobConfigMsg struct {
	cfg model.JobConfig
	err error
}

type jobDetailMsg struct {
	gen   int
	job   model.Job
	slots int // free task slots (-1 unknown), for the advisor
	err   error
}

type backpressureMsg struct {
	vid     string
	level   string
	pct     int       // worst subtask ratio in percent; -1 when unreported
	busy    float64   // avg busyTimeMsPerSecond; -1 when unreported
	wm      int64     // min currentInputWatermark epoch ms; 0 none
	recs    []float64 // per-subtask numRecordsInPerSecond (nil for parallelism 1)
	mboxP99 float64   // worst-subtask mailbox p99 ms; -1 unknown
	softBP  float64   // worst-subtask soft-backpressure ms/s; -1 unknown
	debloat float64   // estimatedTimeToConsumeBuffersMs; -1 when debloating is off
}

type bpInfo struct {
	level   string
	pct     int
	busy    float64 // avg busyTimeMsPerSecond; -1 unknown
	wm      int64   // min currentInputWatermark epoch ms; 0 none
	recs    []float64
	mboxP99 float64 // worst-subtask mailbox p99 ms; -1 unknown
	softBP  float64 // worst-subtask soft-backpressure ms/s; -1 unknown
	debloat float64 // estimatedTimeToConsumeBuffersMs; -1 when debloating is off
}

// JobDetail shows one job's vertices with live backpressure levels.
type JobDetail struct {
	showConfig bool
	jobCfg     *model.JobConfig
	deps       Deps
	jid        string
	name       string
	job        model.Job
	bp         map[string]bpInfo // vertex id -> sampled backpressure
	tbl        *uitable.Model
	bottleneck int // index of the computed bottleneck vertex; -1 none
	gen        int
	applied    int
	slots      int // free slots at last refresh (-1 unknown)
	advice     []analyze.Advice
	err        string
}

// NewJobDetail builds the detail view for one job.
func NewJobDetail(deps Deps, jid, name string) *JobDetail {
	tbl := newTable([]uitable.Column{
		{Title: "VERTEX", Width: -1},
		{Title: "PAR", Width: 4},
		{Title: "STATUS", Width: 11},
		{Title: "BACKPRESSURE", Width: 13},
		{Title: "BUSY", Width: 5},
		{Title: "WM LAG", Width: 8},
		{Title: "SKEW", Width: 10},
		{Title: "REC IN", Width: 10},
		{Title: "REC OUT", Width: 10},
	})
	return &JobDetail{deps: deps, jid: jid, name: name, bp: map[string]bpInfo{}, bottleneck: -1, tbl: tbl}
}

func (d *JobDetail) Title() string   { return d.name }
func (d *JobDetail) Filtering() bool { return false }
func (d *JobDetail) Bindings() []key.Binding {
	return []key.Binding{
		binding("c", "c", "ckpts"),
		binding("x", "x", "exceptions"),
		binding("s", "s", "savepoint"),
		binding("S", "S", "stop+savepoint"),
		binding("ctrl+x", "ctrl-x", "cancel"),
		binding("a", "a", "ai explain"),
		binding("A", "A", "agentic investigate"),
		binding("@", "@", "ask ai"),
		binding("b", "b", "bp history"),
		binding("g", "g", "DAG"),
		binding("L", "L", "job logs"),
		binding("i", "i", "job config"),
		binding("O", "O", "config resolution"),
		binding("W", "W", "what changed"),
		binding("w", "w", "watermarks"),
		binding("t", "t", "subtasks"),
		binding("R", "R", "rescale vertex"),
		binding("d", "d", "skew distribution"),
		binding("esc", "esc", "back"),
	}
}

func (d *JobDetail) Init() tea.Cmd { return d.Refresh() }

func (d *JobDetail) Refresh() tea.Cmd {
	cl := d.deps.CL
	jid := d.jid
	d.gen++
	gen := d.gen
	return func() tea.Msg {
		job, err := cl.Job(jid)
		slots := -1
		if ov, oerr := cl.Overview(); oerr == nil {
			slots = ov.SlotsAvailable
		}
		return jobDetailMsg{gen: gen, job: job, slots: slots, err: err}
	}
}

// backpressureCmds samples backpressure for every vertex in parallel.
func (d *JobDetail) backpressureCmds() tea.Cmd {
	cl := d.deps.CL
	jid := d.jid
	cmds := make([]tea.Cmd, 0, len(d.job.Vertices))
	for _, v := range d.job.Vertices {
		vid := v.ID
		par := v.Parallelism
		cmds = append(cmds, func() tea.Msg {
			bp, err := cl.Backpressure(jid, vid)
			if err != nil {
				return backpressureMsg{vid: vid, level: "", pct: -1, busy: -1}
			}
			busy, err := cl.VertexBusy(jid, vid)
			if err != nil {
				busy = -1
			}
			wm, err := cl.VertexWatermark(jid, vid)
			if err != nil {
				wm = 0
			}
			recs, err := cl.VertexSubtaskRecords(jid, vid, par)
			if err != nil {
				recs = nil
			}
			p99, softBP, debloat, err := cl.VertexMailbox(jid, vid)
			if err != nil {
				p99, softBP, debloat = -1, -1, -1
			}
			return backpressureMsg{vid: vid, level: bp.Level(), pct: bp.MaxRatioPercent(), busy: busy, wm: wm, recs: recs, mboxP99: p99, softBP: softBP, debloat: debloat}
		})
	}
	return tea.Batch(cmds...)
}

func (d *JobDetail) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case jobConfigMsg:
		if msg.err == nil {
			cfg := msg.cfg
			d.jobCfg = &cfg
		}
		return d, nil

	case jobDetailMsg:
		if msg.gen < d.applied {
			return d, nil // out-of-order response from an older tick
		}
		d.applied = msg.gen
		if msg.err != nil {
			d.err = msg.err.Error()
			return d, nil
		}
		d.err = ""
		d.job = msg.job
		d.slots = msg.slots
		d.rebuildRows()
		return d, d.backpressureCmds()

	case backpressureMsg:
		d.bp[msg.vid] = bpInfo{level: msg.level, pct: msg.pct, busy: msg.busy, wm: msg.wm, recs: msg.recs, mboxP99: msg.mboxP99, softBP: msg.softBP, debloat: msg.debloat}
		d.rebuildRows()
		return d, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "c":
			return d, push(NewCheckpoints(d.deps, d.jid, d.name))
		case "x":
			return d, push(NewExceptions(d.deps, d.jid, d.name))
		case "s":
			if g := readOnlyGuard(d.deps); g != nil {
				return d, g
			}
			return d, requestAction(ActionSavepoint, d.jid, d.name)
		case "S":
			if g := readOnlyGuard(d.deps); g != nil {
				return d, g
			}
			return d, requestAction(ActionStop, d.jid, d.name)
		case "ctrl+x":
			if g := readOnlyGuard(d.deps); g != nil {
				return d, g
			}
			return d, requestAction(ActionCancel, d.jid, d.name)
		case "a":
			return d, explainJob(d.deps, d.jid, d.name)
		case "A":
			return d, investigateJob(d.deps, d.jid, d.name)
		case "@":
			return d, askScope(d.deps, d.jid, d.name)
		case "b":
			return d, push(NewBPHistory(d.deps, d.jid, d.name, d.job.Vertices))
		case "g":
			return d, push(NewDagView(d.deps, d.jid, d.name))
		case "L":
			return d, push(NewJobLogs(d.deps, d.jid, d.name))
		case "w":
			return d, push(NewWatermarks(d.deps, d.jid, d.name))
		case "W":
			jid, name, ctx := d.jid, d.name, d.deps.Ctx
			return d, push(NewTextPager(d.deps, "changes("+name+")", func() (string, error) {
				snaps := snapshot.Load(ctx, jid)
				if len(snaps) == 0 {
					return "no config journal yet — snapshots record on every observed change", nil
				}
				var b strings.Builder
				for i := range snaps {
					if i == 0 {
						fmt.Fprintf(&b, "%s  first observed (flink %s)\n", snaps[0].Time.Format("2006-01-02 15:04:05"), snaps[0].FlinkVersion)
						continue
					}
					for _, diff := range snapshot.Diff(snaps[i-1], snaps[i]) {
						fmt.Fprintf(&b, "%s  %s\n", snaps[i].Time.Format("2006-01-02 15:04:05"), diff)
					}
				}
				return b.String(), nil
			}))
		case "O":
			return d, push(NewConfigResolve(d.deps, d.jid, d.name))
		case "i":
			d.showConfig = !d.showConfig
			if d.showConfig && d.jobCfg == nil {
				cl := d.deps.CL
				jid := d.jid
				return d, func() tea.Msg {
					jc, err := cl.JobConfig(jid)
					return jobConfigMsg{cfg: jc, err: err}
				}
			}
			return d, nil
		case "t":
			if len(d.job.Vertices) == 0 {
				return d, nil
			}
			vt := d.job.Vertices[d.tbl.Cursor()]
			return d, push(NewSubtasks(d.deps, d.jid, vt.ID, vt.Name))
		case "d":
			if len(d.job.Vertices) == 0 {
				return d, nil
			}
			v := d.job.Vertices[d.tbl.Cursor()]
			isSource := strings.HasPrefix(v.Name, "Source")
			return d, push(NewSkewDetail(d.deps, d.jid, v.ID, v.Name, v.Parallelism, isSource))
		case "R":
			if g := readOnlyGuard(d.deps); g != nil {
				return d, g
			}
			if len(d.job.Vertices) == 0 {
				return d, nil
			}
			v := d.job.Vertices[d.tbl.Cursor()]
			jid, jobName := d.jid, d.name
			return d, func() tea.Msg {
				return RescaleMsg{JID: jid, Job: jobName, VID: v.ID, Vertex: v.Name, Current: v.Parallelism}
			}
		default:
			d.tbl.Update(msg)
		}
	}
	return d, nil
}

// backpressureCell renders a level as glyph + word (+ worst-subtask percent
// when the cluster reports ratios); the glyph shape carries meaning even
// under NO_COLOR.
func backpressureCell(bp bpInfo) uitable.Cell {
	pct := ""
	if bp.pct >= 0 {
		pct = fmt.Sprintf(" %d%%", bp.pct)
	}
	switch bp.level {
	case "ok":
		return uitable.Styled("● ok"+pct, styles.OKText())
	case "low":
		return uitable.Styled("▅ low"+pct, styles.WarnText())
	case "high":
		return uitable.Styled("█ high"+pct, styles.ErrText())
	case "deprecated":
		return uitable.Styled("n/a", styles.Dim())
	default:
		return uitable.Styled("…", styles.Dim())
	}
}

// wmCell renders watermark lag vs wall clock with severity coloring.
func wmCell(wm int64) uitable.Cell {
	if wm <= 0 {
		return uitable.Styled("-", styles.Dim())
	}
	lag := time.Since(time.UnixMilli(wm))
	text := model.FmtDuration(lag.Milliseconds())
	switch {
	case lag > 30*time.Minute:
		return uitable.Styled(text, styles.ErrText())
	case lag > 5*time.Minute:
		return uitable.Styled(text, styles.WarnText())
	default:
		return uitable.Plain(text)
	}
}

// skewCell renders the FLIP-418 score with a 4-bucket sorted skyline.
func skewCell(recs []float64) uitable.Cell {
	if len(recs) < 2 {
		return uitable.Styled("-", styles.Dim())
	}
	score := analyze.SkewScore(recs)
	glyph := miniSkyline(recs)
	text := fmt.Sprintf("%s %d%%", glyph, score)
	switch {
	case score > 60:
		return uitable.Styled(text, styles.ErrText())
	case score >= 30:
		return uitable.Styled(text, styles.WarnText())
	default:
		return uitable.Styled(text, styles.OKText())
	}
}

var skylineRunes = []rune("▁▂▃▄▅▆▇█")

func miniSkyline(values []float64) string {
	buckets := analyze.Skyline(values, 4)
	maxV := 0.0
	for _, v := range buckets {
		if v > maxV {
			maxV = v
		}
	}
	if maxV == 0 {
		maxV = 1
	}
	out := make([]rune, len(buckets))
	for i, v := range buckets {
		idx := int(v / maxV * float64(len(skylineRunes)-1))
		out[i] = skylineRunes[idx]
	}
	return string(out)
}

func busyCell(busy float64) uitable.Cell {
	if busy < 0 {
		return uitable.Styled("-", styles.Dim())
	}
	pct := int(busy / 10) // busyTimeMsPerSecond: 1000 == 100%
	switch {
	case pct >= 80:
		return uitable.Styled(fmt.Sprintf("%d%%", pct), styles.ErrText())
	case pct >= 50:
		return uitable.Styled(fmt.Sprintf("%d%%", pct), styles.WarnText())
	default:
		return uitable.Plain(fmt.Sprintf("%d%%", pct))
	}
}

func (d *JobDetail) rebuildRows() {
	samples := make([]analyze.VertexSample, len(d.job.Vertices))
	for i, v := range d.job.Vertices {
		bp := d.bp[v.ID]
		busy := bp.busy
		if busy == 0 && bp.level == "" {
			busy = -1 // not sampled yet
		}
		samples[i] = analyze.VertexSample{ID: v.ID, Name: v.Name, Level: bp.level, BusyMsPerSec: busy, Parallelism: v.Parallelism}
	}
	d.bottleneck = analyze.Bottleneck(samples)
	d.advice = analyze.Advise(samples, d.slots)

	rows := make([][]uitable.Cell, 0, len(d.job.Vertices))
	var keys []string
	for i, v := range d.job.Vertices {
		bp := d.bp[v.ID]
		name := uitable.Plain(v.Name)
		if i == d.bottleneck {
			name = uitable.Styled("⚑ "+v.Name, styles.WarnText())
		}
		keys = append(keys, v.ID)
		rows = append(rows, []uitable.Cell{
			name,
			uitable.Plain(fmt.Sprint(v.Parallelism)),
			uitable.Styled(v.Status, styles.StateStyle(v.Status)),
			backpressureCell(bp),
			busyCell(bp.busy),
			wmCell(bp.wm),
			skewCell(bp.recs),
			uitable.Plain(fmt.Sprint(v.Metrics.ReadRecords)),
			uitable.Plain(fmt.Sprint(v.Metrics.WriteRecords)),
		})
	}
	d.tbl.SetRowsKeyed(rows, keys)
}

func (d *JobDetail) View(width, height int) string {
	return framed(width, height, d.name, -1, d.job.State+" · up "+model.FmtDuration(d.job.Duration), d.viewBody)
}

func (d *JobDetail) viewBody(width, height int) string {
	// Full JID on the first line on purpose: it's what gets pasted into flink
	// CLI commands (the job name is now in the frame border).
	out := " " + styles.Dim().Render(d.jid) + "  " +
		styles.StateStyle(d.job.State).Render(d.job.State) +
		styles.Dim().Render(" · up "+model.FmtDuration(d.job.Duration)) + "\n"
	used := 1
	if d.showConfig && d.jobCfg != nil {
		ec := d.jobCfg.ExecutionConfig
		out += styles.Dim().Render(fmt.Sprintf(" restart-strategy: %s · job-parallelism %d · object-reuse %v · user-config keys %d",
			ec.RestartStrategy, ec.JobParallelism, ec.ObjectReuse, len(ec.UserConfig))) + "\n"
		used++
	}
	if d.bottleneck >= 0 && d.bottleneck < len(d.job.Vertices) {
		out += styles.WarnText().Render("⚑ bottleneck: "+d.job.Vertices[d.bottleneck].Name) +
			styles.Dim().Render("  (busy but not backpressured — the slow stage)") + "\n"
		used++
		for _, adv := range d.advice {
			style := styles.WarnText()
			if adv.Severity == "info" {
				style = styles.Dim()
			}
			out += style.Render("  ↳ "+adv.Text) + "\n"
			used++
		}
	}
	// pre-backpressure early warning: soft backpressure or a hot mailbox
	// while the sampled level still reads OK — buffers are filling and hard
	// backpressure is the likely next state.
	for _, v := range d.job.Vertices {
		bp := d.bp[v.ID]
		if bp.level != "" && bp.level != "ok" && bp.level != "OK" {
			continue // already visibly backpressured; the column shows it
		}
		if bp.softBP >= 100 || bp.mboxP99 >= 50 || bp.debloat >= 200 {
			detail := ""
			if bp.softBP >= 0 {
				detail = fmt.Sprintf("soft-bp %dms/s", int(bp.softBP))
			}
			if bp.mboxP99 >= 50 {
				if detail != "" {
					detail += " · "
				}
				detail += fmt.Sprintf("mailbox p99 %dms", int(bp.mboxP99))
			}
			if bp.debloat >= 200 {
				if detail != "" {
					detail += " · "
				}
				detail += fmt.Sprintf("~%dms of buffered data queued (debloat estimate)", int(bp.debloat))
			}
			out += styles.WarnText().Render("⚠ early warning: "+v.Name) +
				styles.Dim().Render("  buffers filling ("+detail+") — backpressure likely next") + "\n"
			used++
			break // one banner; the worst offender is enough
		}
	}
	if d.err != "" {
		out += styles.ErrText().Render("⚠ "+d.err) + "\n"
		used++
	}
	d.tbl.SetSize(width, height-used)
	return out + d.tbl.View()
}
