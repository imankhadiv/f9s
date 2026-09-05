package views

import (
	"fmt"
	"sort"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type stragglerRow struct {
	vertex string
	st     model.CheckpointSubtask
}

type stragglersMsg struct {
	rows []stragglerRow
	err  error
}

// Stragglers breaks one checkpoint down per subtask: who acked last, and
// whether barrier alignment, sync/async snapshot time, or start delay
// dominated — the diagnostic split the Web UI only shows in aggregate.
type Stragglers struct {
	deps Deps
	jid  string
	cpID int64
	tbl  *uitable.Model
	rows []stragglerRow
	err  string
}

// NewStragglers builds the per-subtask breakdown for one checkpoint.
func NewStragglers(deps Deps, jid string, cpID int64) *Stragglers {
	tbl := newTable([]uitable.Column{
		{Title: "VERTEX", Width: -1},
		{Title: "SUB", Width: 4},
		{Title: "E2E", Width: 8},
		{Title: "ALIGN", Width: 8},
		{Title: "SYNC", Width: 7},
		{Title: "ASYNC", Width: 7},
		{Title: "DELAY", Width: 7},
		{Title: "SIZE", Width: 9},
		{Title: "IN-FLIGHT", Width: 9},
	})
	return &Stragglers{deps: deps, jid: jid, cpID: cpID, tbl: tbl}
}

func (v *Stragglers) Title() string   { return fmt.Sprintf("ckpt %d", v.cpID) }
func (v *Stragglers) Filtering() bool { return false }
func (v *Stragglers) Bindings() []key.Binding {
	return []key.Binding{binding("esc", "esc", "back")}
}

func (v *Stragglers) Init() tea.Cmd { return v.Refresh() }

func (v *Stragglers) Refresh() tea.Cmd {
	cl := v.deps.CL
	jid, cpID := v.jid, v.cpID
	return func() tea.Msg {
		job, err := cl.Job(jid)
		if err != nil {
			return stragglersMsg{err: err}
		}
		var rows []stragglerRow
		for _, vx := range job.Vertices {
			subs, err := cl.CheckpointSubtasks(jid, cpID, vx.ID)
			if err != nil {
				continue // vertex may not participate in this checkpoint
			}
			for _, st := range subs {
				rows = append(rows, stragglerRow{vertex: vx.Name, st: st})
			}
		}
		sort.SliceStable(rows, func(a, b int) bool { return rows[a].st.E2E > rows[b].st.E2E })
		if len(rows) > 200 {
			rows = rows[:200]
		}
		return stragglersMsg{rows: rows}
	}
}

func (v *Stragglers) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case stragglersMsg:
		if msg.err != nil {
			v.err = msg.err.Error()
			return v, nil
		}
		v.err = ""
		v.rows = msg.rows
		rows := make([][]uitable.Cell, 0, len(msg.rows))
		keys := make([]string, 0, len(msg.rows))
		for i, r := range msg.rows {
			st := r.st
			e2eStyle := styles.OKText()
			if i == 0 && st.E2E > 0 {
				e2eStyle = styles.ErrText() // the straggler
			}
			alignStyle := styles.Dim()
			if st.Alignment.Duration > st.E2E/2 && st.E2E > 100 {
				alignStyle = styles.WarnText() // barrier wait dominates
			}
			keys = append(keys, fmt.Sprintf("%s/%d", r.vertex, st.Index))
			rows = append(rows, []uitable.Cell{
				uitable.Plain(r.vertex),
				uitable.Plain(fmt.Sprintf("#%d", st.Index)),
				uitable.Styled(fmtDurMs(st.E2E), e2eStyle),
				uitable.Styled(fmtDurMs(st.Alignment.Duration), alignStyle),
				uitable.Styled(fmtDurMs(st.Checkpoint.Sync), styles.Dim()),
				uitable.Styled(fmtDurMs(st.Checkpoint.Async), styles.Dim()),
				uitable.Styled(fmtDurMs(st.StartDelay), styles.Dim()),
				uitable.Plain(model.FmtBytes(st.StateSize)),
				uitable.Styled(model.FmtBytes(st.Alignment.Persisted), styles.Dim()),
			})
		}
		v.tbl.SetRowsKeyed(rows, keys)
		return v, nil
	case tea.KeyMsg:
		v.tbl.Update(msg)
	}
	return v, nil
}

// fmtDurMs keeps millisecond precision below one second — checkpoint
// phases are often single-digit ms and "0s" hides the whole story.
func fmtDurMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return model.FmtDuration(ms)
}

func (v *Stragglers) View(width, height int) string {
	out := styles.ViewTitle().Render(fmt.Sprintf("Checkpoint %d — per-subtask breakdown", v.cpID)) +
		styles.Dim().Render("  slowest first · ALIGN amber = barrier wait dominated") + "\n"
	used := 1
	if v.err != "" {
		out += styles.ErrText().Render("⚠ "+v.err) + "\n"
		used++
	}
	v.tbl.SetSize(width, height-used)
	return out + v.tbl.View()
}
