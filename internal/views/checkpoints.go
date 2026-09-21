package views

import (
	"fmt"
	"github.com/charmbracelet/bubbles/key"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type checkpointsMsg struct {
	cp  model.Checkpoints
	cfg model.CheckpointConfig
	err error
}

// CheckpointsView shows checkpoint stats and history for one job.
type CheckpointsView struct {
	deps Deps
	jid  string
	name string
	cp   model.Checkpoints
	cfg  model.CheckpointConfig
	tbl  *uitable.Model
	err  string
}

// NewCheckpoints builds the checkpoints view for one job.
func NewCheckpoints(deps Deps, jid, name string) *CheckpointsView {
	tbl := newTable([]uitable.Column{
		{Title: "ID", Width: 6, Right: true},
		{Title: "STATUS", Width: 12},
		{Title: "SP", Width: 3},
		{Title: "AGE", Width: 8, Right: true},
		{Title: "DURATION", Width: 9, Right: true},
		{Title: "SIZE", Width: 9, Right: true},
		{Title: "DELTA", Width: 14, Right: true},
		{Title: "ACK", Width: 7, Right: true},
		{Title: "FAILURE", Width: -1},
	})
	return &CheckpointsView{deps: deps, jid: jid, name: name, tbl: tbl}
}

func (c *CheckpointsView) Title() string   { return "checkpoints" }
func (c *CheckpointsView) Filtering() bool { return false }
func (c *CheckpointsView) Bindings() []key.Binding {
	return []key.Binding{
		binding("enter", "enter", "per-subtask breakdown"),
		binding("c", "c", "compare with previous checkpoints"),
		binding("esc", "esc", "back"),
	}
}

func (c *CheckpointsView) Init() tea.Cmd { return c.Refresh() }

func (c *CheckpointsView) Refresh() tea.Cmd {
	cl := c.deps.CL
	jid := c.jid
	return func() tea.Msg {
		cp, err := cl.Checkpoints(jid)
		cfg, _ := cl.CheckpointConfig(jid)
		return checkpointsMsg{cp: cp, cfg: cfg, err: err}
	}
}

func (c *CheckpointsView) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case checkpointsMsg:
		if msg.err != nil {
			c.err = msg.err.Error()
			return c, nil
		}
		c.err = ""
		c.cp = msg.cp
		c.cfg = msg.cfg
		rows := make([][]uitable.Cell, 0, len(c.cp.History))
		for _, h := range c.cp.History {
			sp := ""
			if h.IsSavepoint {
				sp = "✓"
			}
			ackStyle := styles.OKText()
			if h.NumAcknowledged < h.NumSubtasks {
				ackStyle = styles.WarnText()
			}
			rows = append(rows, []uitable.Cell{
				uitable.Plain(fmt.Sprint(h.ID)),
				uitable.Styled(h.Status, styles.StateStyle(h.Status)),
				uitable.Styled(sp, styles.OKText()),
				uitable.Plain(model.FmtAge(h.TriggerTimestamp)),
				uitable.Plain(model.FmtDuration(h.EndToEndDuration)),
				uitable.Plain(model.FmtBytes(h.StateSize)),
				deltaCell(h),
				uitable.Styled(fmt.Sprintf("%d/%d", h.NumAcknowledged, h.NumSubtasks), ackStyle),
				uitable.Styled(h.FailureMessage, styles.ErrText()),
			})
		}
		c.tbl.SetRows(rows)
		return c, nil
	case tea.KeyMsg:
		if msg.String() == "c" {
			if ids := c.compareIDs(); len(ids) >= 2 {
				return c, push(NewCheckpointCompare(c.deps, c.jid, c.name, ids))
			}
			return c, flashErrV("need at least two completed checkpoints from the selected one back to compare")
		}
		if msg.String() == "enter" && len(c.cp.History) > 0 {
			cur := c.tbl.Cursor()
			if cur < len(c.cp.History) {
				h := c.cp.History[cur]
				return c, push(NewStragglers(c.deps, c.jid, h.ID))
			}
		}
		c.tbl.Update(msg)
	}
	return c, nil
}

// compareIDs picks the selected checkpoint (if completed) and the completed,
// non-savepoint checkpoints before it: up to compareN, newest first.
func (c *CheckpointsView) compareIDs() []int64 {
	start := c.tbl.Cursor()
	if c.tbl.Len() == 0 || start < 0 || start >= len(c.cp.History) {
		return nil
	}
	var ids []int64
	for _, h := range c.cp.History[start:] { // history is newest first
		if h.Status == "COMPLETED" && !h.IsSavepoint {
			ids = append(ids, h.ID)
			if len(ids) == compareN {
				break
			}
		}
	}
	return ids
}

// deltaCell shows what a checkpoint uploaded (checkpointed_size) and its
// share of the full state — small for healthy incremental checkpoints.
func deltaCell(h model.Checkpoint) uitable.Cell {
	if h.CheckpointedSize <= 0 || h.StateSize <= 0 {
		return uitable.Styled("-", styles.Dim())
	}
	p := h.CheckpointedSize * 100 / h.StateSize
	return uitable.Styled(fmt.Sprintf("%s %3d%%", model.FmtBytes(h.CheckpointedSize), p), styles.Dim())
}

// healthLines renders the checkpoint-health lines under the config line:
// incremental delta, in-flight data, retention on cancel, failure budget.
func (c *CheckpointsView) healthLines() []string {
	var out []string
	inc := analyze.IncrementalHealth(c.cp.History, c.cfg.StateBackend)
	if sum := inc.Summary(); sum != "" {
		st := styles.Dim()
		if inc.FullUpload {
			st = styles.WarnText()
		}
		out = append(out, st.Render(" "+sum))
	}
	if sm := c.cp.Summary; sm.AlignmentBuffer.Max > 0 || sm.PersistedData.Max > 0 {
		out = append(out, styles.Dim().Render(fmt.Sprintf(" in-flight   aligned-buffered p50 %s · max %s · persisted (unaligned) p50 %s · max %s",
			model.FmtBytes(int64(sm.AlignmentBuffer.P50)), model.FmtBytes(int64(sm.AlignmentBuffer.Max)),
			model.FmtBytes(int64(sm.PersistedData.P50)), model.FmtBytes(int64(sm.PersistedData.Max)))))
	}
	if txt, warn := analyze.Retention(c.cfg); warn {
		out = append(out, styles.WarnText().Render(" ⚠ "+txt))
	} else {
		out = append(out, styles.Dim().Render(" "+txt))
	}
	txt, level := analyze.Tolerance(c.cfg.TolerableFailed, analyze.FailureStreak(c.cp.History))
	switch level {
	case "crit":
		out = append(out, styles.ErrText().Render(" ⚠ "+txt))
	case "warn":
		out = append(out, styles.WarnText().Render(" "+txt))
	default:
		out = append(out, styles.Dim().Render(" "+txt))
	}
	return out
}

// backendBadge paints the state backend with disaggregated-state awareness:
// ForSt (Flink 2.x remote state) gets a loud badge, changelog gets a tag.
func backendBadge(cfg model.CheckpointConfig) string {
	b := cfg.StateBackend
	var out string
	switch {
	case strings.Contains(b, "ForSt"):
		out = styles.WarnText().Render("⬡ " + b + " (disaggregated)")
	case strings.Contains(b, "RocksDB"):
		out = styles.OKText().Render(b)
	default:
		out = styles.Dim().Render(b)
	}
	if cfg.ChangelogEnabled {
		out += styles.WarnText().Render(" · changelog:" + cfg.ChangelogStorage)
	}
	return out
}

// AIContext summarizes the on-screen checkpoint state for the AI panel.
func (c *CheckpointsView) AIContext() (string, []string) {
	var b strings.Builder
	fmt.Fprintf(&b, "## On screen: checkpoints for %s\n", c.name)
	cn := c.cp.Counts
	fmt.Fprintf(&b, "counts: %d completed · %d failed · %d in-progress · %d restored\n",
		cn.Completed, cn.Failed, cn.InProgress, cn.Restored)
	if c.cfg.Mode != "" {
		align := "aligned"
		if c.cfg.UnalignedEnabled {
			align = "unaligned"
		}
		fmt.Fprintf(&b, "config: %s · every %s · timeout %s · %s barriers · backend %s\n",
			c.cfg.Mode, model.FmtDuration(c.cfg.Interval), model.FmtDuration(c.cfg.Timeout),
			align, c.cfg.StateBackend)
		ret, _ := analyze.Retention(c.cfg)
		tol, _ := analyze.Tolerance(c.cfg.TolerableFailed, analyze.FailureStreak(c.cp.History))
		fmt.Fprintf(&b, "%s\n%s\n", ret, tol)
		if sum := analyze.IncrementalHealth(c.cp.History, c.cfg.StateBackend).Summary(); sum != "" {
			b.WriteString(sum + "\n")
		}
	}
	if len(c.cp.History) > 0 {
		b.WriteString("recent (newest first):\n")
		for i, h := range c.cp.History {
			if i >= 5 {
				break
			}
			sp := ""
			if h.IsSavepoint {
				sp = " savepoint"
			}
			fmt.Fprintf(&b, "  #%d %s%s · age %s · dur %s · size %s · ack %d/%d",
				h.ID, h.Status, sp, model.FmtAge(h.TriggerTimestamp),
				model.FmtDuration(h.EndToEndDuration), model.FmtBytes(h.StateSize),
				h.NumAcknowledged, h.NumSubtasks)
			if h.FailureMessage != "" {
				fmt.Fprintf(&b, " · fail: %s", h.FailureMessage)
			}
			b.WriteString("\n")
		}
	} else {
		b.WriteString("no checkpoint history loaded yet\n")
	}
	return b.String(), []string{c.name}
}

func (c *CheckpointsView) View(width, height int) string {
	counts := c.cp.Counts
	right := fmt.Sprintf(
		"completed %d · failed %d · in-progress %d · restored %d",
		counts.Completed, counts.Failed, counts.InProgress, counts.Restored)
	return framed(width, height, "Checkpoints", counts.Total, right, func(iw, ih int) string {
		out := ""
		used := 0
		if c.cfg.Mode != "" {
			unaligned := "aligned"
			if c.cfg.UnalignedEnabled {
				unaligned = "unaligned"
			}
			out += styles.Dim().Render(fmt.Sprintf(" %s · every %s · timeout %s · %s barriers · ",
				c.cfg.Mode, model.FmtDuration(c.cfg.Interval), model.FmtDuration(c.cfg.Timeout),
				unaligned)) + backendBadge(c.cfg) + "\n"
			used++
			for _, l := range c.healthLines() {
				out += l + "\n"
				used++
			}
			if f := c.cp.Latest.Failed; f.FailureTimestamp > 0 {
				msg := f.FailureMessage
				if msg == "" {
					msg = "no message reported"
				}
				ack := ""
				if f.NumSubtasks > 0 {
					ack = fmt.Sprintf(" · acked %d/%d", f.NumAcknowledged, f.NumSubtasks)
				}
				out += styles.ErrText().Render(fmt.Sprintf(" ⚠ last checkpoint FAILED %s ago", model.FmtAge(f.FailureTimestamp))) +
					styles.Dim().Render(fmt.Sprintf(" (id %d%s) — %s", f.ID, ack, truncate(msg, 80))) + "\n"
				used++
			}
			var durations []int64
			for _, cp := range c.cp.History { // newest first
				if cp.Status == "COMPLETED" && !cp.IsSavepoint {
					durations = append(durations, cp.EndToEndDuration)
				}
			}
			for _, ad := range analyze.CheckpointAdvice(durations, c.cfg.Interval, c.cfg.Timeout, c.cfg.UnalignedEnabled) {
				out += styles.WarnText().Render(" ⚑ "+ad.Text) + "\n"
				used++
			}
			if sm := c.cp.Summary; sm.EndToEndDuration.Max > 0 {
				out += styles.Dim().Render(fmt.Sprintf(" duration  p50 %s · p90 %s · p99 %s · p999 %s · max %s",
					model.FmtDuration(int64(sm.EndToEndDuration.P50)), model.FmtDuration(int64(sm.EndToEndDuration.P90)),
					model.FmtDuration(int64(sm.EndToEndDuration.P99)), model.FmtDuration(int64(sm.EndToEndDuration.P999)),
					model.FmtDuration(int64(sm.EndToEndDuration.Max)))) + "\n"
				out += styles.Dim().Render(fmt.Sprintf(" size      p50 %s · p90 %s · p99 %s · max %s",
					model.FmtBytes(int64(sm.StateSize.P50)), model.FmtBytes(int64(sm.StateSize.P90)),
					model.FmtBytes(int64(sm.StateSize.P99)), model.FmtBytes(int64(sm.StateSize.Max)))) + "\n"
				used += 2
			}
		}
		if n := len(c.cp.History); n >= 3 {
			sizes := make([]float64, 0, n)
			maxS := 0.0
			for i := n - 1; i >= 0; i-- { // oldest → newest
				v := float64(c.cp.History[i].StateSize)
				sizes = append(sizes, v)
				if v > maxS {
					maxS = v
				}
			}
			out += " " + styles.Dim().Render("state size ") + styles.OKText().Render(spark(sizes, maxS, 40)) + "\n"
			used++
			// quantified growth verdict (least-squares over completed ckpts)
			var gp []analyze.GrowthPoint
			for i := len(c.cp.History) - 1; i >= 0; i-- {
				h := c.cp.History[i]
				if h.Status == "COMPLETED" && !h.IsSavepoint {
					gp = append(gp, analyze.GrowthPoint{T: time.UnixMilli(h.TriggerTimestamp), Size: h.StateSize})
				}
			}
			vol := int64(c.deps.Cfg.Contexts[c.deps.Ctx].StateVolumeGB) << 30
			gv := analyze.StateGrowth(gp, vol)
			gvStyle := styles.Dim()
			if gv.Trend == "growing" {
				gvStyle = styles.WarnText()
				if gv.EtaDays > 0 && gv.EtaDays < 7 {
					gvStyle = styles.ErrText()
				}
			}
			out += " " + gvStyle.Render("▲ "+gv.Summary()) + "\n"
			used++
		}
		if c.err != "" {
			out += styles.ErrText().Render("⚠ "+c.err) + "\n"
			used++
		}
		if c.tbl.Len() == 0 {
			return out + styles.Dim().Render("No checkpoints yet — check execution.checkpointing.interval on the job.")
		}
		c.tbl.SetSize(iw, ih-used)
		return out + c.tbl.View()
	})
}
