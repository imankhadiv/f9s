package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

// compareN is how many completed checkpoints the comparison lines up.
const compareN = 6

type ckptCompareMsg struct {
	details []model.CheckpointDetail // newest first
	names   map[string]string        // vid → vertex name
	err     error
}

// CheckpointCompare lines up the last N completed checkpoints per vertex —
// which operator made the checkpoint bigger or slower?
type CheckpointCompare struct {
	deps    Deps
	jid     string
	name    string
	ids     []int64 // newest first
	metric  analyze.CkptMetric
	details []model.CheckpointDetail
	names   map[string]string
	tbl     *uitable.Model
	loaded  bool
	err     string
}

// NewCheckpointCompare compares the given checkpoints (newest first).
func NewCheckpointCompare(deps Deps, jid, name string, ids []int64) *CheckpointCompare {
	c := &CheckpointCompare{deps: deps, jid: jid, name: name, ids: ids}
	cols := []uitable.Column{{Title: "VERTEX", Width: -1}}
	for i := len(ids) - 1; i >= 0; i-- { // oldest → newest, left to right
		cols = append(cols, uitable.Column{Title: fmt.Sprintf("#%d", ids[i]), Width: 9, Right: true})
	}
	cols = append(cols, uitable.Column{Title: "TREND", Width: max(len(ids)+1, 6)}, uitable.Column{Title: "CHANGE", Width: 8, Right: true})
	c.tbl = newTable(cols)
	return c
}

func (c *CheckpointCompare) Title() string   { return "ckpt compare" }
func (c *CheckpointCompare) Filtering() bool { return false }
func (c *CheckpointCompare) Bindings() []key.Binding {
	return []key.Binding{binding("m", "m", "metric: size/duration/uploaded/alignment"), binding("esc", "esc", "back")}
}

func (c *CheckpointCompare) Init() tea.Cmd {
	cl, jid, ids := c.deps.CL, c.jid, c.ids
	return func() tea.Msg {
		names := map[string]string{}
		if job, err := cl.Job(jid); err == nil {
			for _, v := range job.Vertices {
				names[v.ID] = v.Name
			}
		}
		var ds []model.CheckpointDetail
		for _, id := range ids {
			d, err := cl.CheckpointDetail(jid, id)
			if err != nil {
				return ckptCompareMsg{err: err}
			}
			ds = append(ds, d)
		}
		return ckptCompareMsg{details: ds, names: names}
	}
}

// Refresh does nothing: completed checkpoints never change.
func (c *CheckpointCompare) Refresh() tea.Cmd { return nil }

func (c *CheckpointCompare) fmtVal(v int64) string {
	switch {
	case v < 0:
		return "-"
	case c.metric.IsBytes():
		return model.FmtBytes(v)
	default:
		return model.FmtDuration(v)
	}
}

func (c *CheckpointCompare) rebuild() {
	trends := analyze.CompareCheckpoints(c.details, c.metric)
	rows := make([][]uitable.Cell, 0, len(trends))
	for i, tr := range trends {
		name := c.names[tr.VID]
		if name == "" {
			name = model.ShortJID(tr.VID)
		}
		row := []uitable.Cell{uitable.Plain(name)}
		vals := make([]float64, 0, len(tr.Values))
		for _, v := range tr.Values {
			row = append(row, uitable.Plain(c.fmtVal(v)))
			if v >= 0 {
				vals = append(vals, float64(v))
			}
		}
		change := uitable.Styled("-", styles.Dim())
		if tr.Values[0] > 0 && tr.Values[len(tr.Values)-1] >= 0 {
			st := styles.Dim()
			switch {
			case i == 0 && tr.Change >= 0.25:
				st = styles.ErrText() // the top grower, and growing meaningfully
			case tr.Change >= 0.25:
				st = styles.WarnText()
			}
			change = uitable.Styled(fmt.Sprintf("%+.0f%%", tr.Change*100), st)
		}
		row = append(row, uitable.Styled(styles.Spark(vals), styles.Dim()), change)
		rows = append(rows, row)
	}
	c.tbl.SetRows(rows)
}

func (c *CheckpointCompare) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ckptCompareMsg:
		c.loaded = true
		if msg.err != nil {
			c.err = msg.err.Error()
			return c, nil
		}
		c.details, c.names = msg.details, msg.names
		c.rebuild()
		return c, nil
	case tea.KeyMsg:
		if msg.String() == "m" {
			c.metric = c.metric.Next()
			c.rebuild()
			return c, nil
		}
		c.tbl.Update(msg)
	}
	return c, nil
}

// headline names the vertex that grew the most, in words.
func (c *CheckpointCompare) headline() string {
	trends := analyze.CompareCheckpoints(c.details, c.metric)
	if len(trends) == 0 || trends[0].Delta <= 0 {
		return fmt.Sprintf("no vertex grew in %s across these checkpoints", c.metric)
	}
	top := trends[0]
	name := c.names[top.VID]
	if name == "" {
		name = model.ShortJID(top.VID)
	}
	first, last := top.Values[0], top.Values[len(top.Values)-1]
	return fmt.Sprintf("▲ %s grew the most in %s: %s → %s (%+.0f%%)", name, c.metric, c.fmtVal(first), c.fmtVal(last), top.Change*100)
}

// AIContext hands the AI the per-vertex comparison.
func (c *CheckpointCompare) AIContext() (string, []string) {
	var b strings.Builder
	fmt.Fprintf(&b, "## On screen: per-vertex %s across checkpoints %v (oldest→newest) of %s\n", c.metric, reverseIDs(c.ids), c.name)
	for _, tr := range analyze.CompareCheckpoints(c.details, c.metric) {
		vals := make([]string, len(tr.Values))
		for i, v := range tr.Values {
			vals[i] = c.fmtVal(v)
		}
		fmt.Fprintf(&b, "  %s: %s (change %+.0f%%)\n", orDash(c.names[tr.VID]), strings.Join(vals, " → "), tr.Change*100)
	}
	return b.String(), []string{c.name}
}

func reverseIDs(ids []int64) []int64 {
	out := make([]int64, len(ids))
	for i, id := range ids {
		out[len(ids)-1-i] = id
	}
	return out
}

func (c *CheckpointCompare) View(width, height int) string {
	right := "metric: " + c.metric.String() + " · <m> cycles"
	return framed(width, height, "Checkpoint compare · "+c.name, c.tbl.Len(), right, func(iw, ih int) string {
		if c.err != "" {
			return errorBanner(c.err)
		}
		if !c.loaded {
			return emptyState(fmt.Sprintf(" loading %d checkpoint details…", len(c.ids)))
		}
		if c.tbl.Len() == 0 {
			return emptyState(" These checkpoints carry no per-vertex details (the JobManager may have dropped them from its history).")
		}
		out := styles.WarnText().Render(" "+c.headline()) + "\n"
		c.tbl.SetSize(iw, ih-1)
		return out + c.tbl.View()
	})
}
