package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type subtasksMsg struct {
	subs []model.SubtaskInfo
	err  error
}

// Subtasks is the Web UI's subtask table, sharpened: per-subtask attempt
// number, TaskManager, lifecycle durations (a subtask stuck INITIALIZING
// shows itself), and best-effort prior-attempt history on enter.
type Subtasks struct {
	deps   Deps
	jid    string
	vid    string
	vertex string
	subs   []model.SubtaskInfo
	tbl    *uitable.Model
	err    string
}

// NewSubtasks builds the subtask table for one vertex.
func NewSubtasks(deps Deps, jid, vid, vertex string) *Subtasks {
	tbl := newTable([]uitable.Column{
		{Title: "SUB", Width: 4},
		{Title: "ATTEMPT", Width: 8},
		{Title: "STATUS", Width: 12},
		{Title: "TASKMANAGER", Width: 24},
		{Title: "UPTIME", Width: 9},
		{Title: "DEPLOY", Width: 8},
		{Title: "INIT", Width: 8},
		{Title: "ENDPOINT", Width: -1},
	})
	return &Subtasks{deps: deps, jid: jid, vid: vid, vertex: vertex, tbl: tbl}
}

func (v *Subtasks) Title() string   { return "subtasks" }
func (v *Subtasks) Filtering() bool { return false }
func (v *Subtasks) Bindings() []key.Binding {
	return []key.Binding{
		binding("enter", "enter", "attempt history"),
		binding("esc", "esc", "back"),
	}
}

func (v *Subtasks) Init() tea.Cmd { return v.Refresh() }

func (v *Subtasks) Refresh() tea.Cmd {
	cl := v.deps.CL
	jid, vid := v.jid, v.vid
	return func() tea.Msg {
		subs, err := cl.VertexSubtasks(jid, vid)
		return subtasksMsg{subs: subs, err: err}
	}
}

func (v *Subtasks) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case subtasksMsg:
		if msg.err != nil {
			v.err = msg.err.Error()
			return v, nil
		}
		v.err = ""
		v.subs = msg.subs
		rows := make([][]uitable.Cell, 0, len(msg.subs))
		keys := make([]string, 0, len(msg.subs))
		for _, st := range msg.subs {
			keys = append(keys, fmt.Sprint(st.Subtask))
			attempt := fmt.Sprint(st.Attempt)
			attemptStyle := styles.Dim()
			if st.Attempt > 0 {
				attemptStyle = styles.WarnText() // restarts happened here
			}
			rows = append(rows, []uitable.Cell{
				uitable.Plain(fmt.Sprintf("#%d", st.Subtask)),
				uitable.Styled(attempt, attemptStyle),
				uitable.Styled(st.Status, styles.StateStyle(st.Status)),
				uitable.Styled(st.TaskManagerID, styles.Dim()),
				uitable.Plain(model.FmtDuration(st.Duration)),
				uitable.Styled(fmt.Sprintf("%dms", st.StatusDuration["DEPLOYING"]), styles.Dim()),
				uitable.Styled(fmt.Sprintf("%dms", st.StatusDuration["INITIALIZING"]), styles.Dim()),
				uitable.Styled(st.Endpoint, styles.Dim()),
			})
		}
		v.tbl.SetRowsKeyed(rows, keys)
		return v, nil
	case tea.KeyMsg:
		if msg.String() == "enter" && len(v.subs) > 0 {
			st := v.subs[v.tbl.Cursor()]
			cl := v.deps.CL
			jid, vid := v.jid, v.vid
			title := fmt.Sprintf("attempts(sub %d)", st.Subtask)
			cur := st
			return v, push(NewTextPager(v.deps, title, func() (string, error) {
				return renderAttemptHistory(cl, jid, vid, cur), nil
			}))
		}
		v.tbl.Update(msg)
	}
	return v, nil
}

// renderAttemptHistory fetches whatever prior attempts the cluster still
// retains (best-effort — Flink drops them across execution-graph resets).
func renderAttemptHistory(cl clientIface, jid, vid string, cur model.SubtaskInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "subtask #%d — current attempt %d (%s on %s, up %s)\n\n",
		cur.Subtask, cur.Attempt, cur.Status, cur.TaskManagerID, model.FmtDuration(cur.Duration))
	found := 0
	lo := cur.Attempt - 6
	if lo < 0 {
		lo = 0
	}
	for a := cur.Attempt - 1; a >= lo; a-- {
		at, err := cl.SubtaskAttempt(jid, vid, cur.Subtask, a)
		if err != nil {
			continue
		}
		found++
		fmt.Fprintf(&b, "attempt %d  %-10s tm=%s  started %s  lived %s\n",
			at.Attempt, at.Status, at.TaskManagerID,
			model.FmtAge(at.StartTime)+" ago", model.FmtDuration(at.Duration))
	}
	if found == 0 {
		b.WriteString("no prior attempts retained by the cluster — Flink keeps them only\nwithin the current execution-graph generation (restarts via rescale or\nfull failover reset the history).\n")
	}
	return b.String()
}

// clientIface is the slice of client used here (mockable in tests).
type clientIface interface {
	SubtaskAttempt(jid, vid string, subtask, attempt int) (model.SubtaskInfo, error)
}

func (v *Subtasks) View(width, height int) string {
	out := styles.ViewTitle().Render(fmt.Sprintf("Subtasks[%d] · %s", v.tbl.Len(), v.vertex)) + "\n"
	used := 1
	if v.err != "" {
		out += styles.ErrText().Render("⚠ "+v.err) + "\n"
		used++
	}
	v.tbl.SetSize(width, height-used)
	return out + v.tbl.View()
}
