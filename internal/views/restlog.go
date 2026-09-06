package views

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type restLogMsg struct{ calls []client.CallRecord }

// RestLog shows the REST calls f9s has issued — trust and education, the
// lazygit command-log pattern (DESIGN.md #6).
type RestLog struct {
	deps Deps
	tbl  *uitable.Model
}

// NewRestLog builds the REST-call log view.
func NewRestLog(deps Deps) *RestLog {
	tbl := newTable([]uitable.Column{
		{Title: "TIME", Width: 8},
		{Title: "METHOD", Width: 6},
		{Title: "PATH", Width: -1},
		{Title: "STATUS", Width: 14},
		{Title: "DUR", Width: 7, Right: true},
	})
	return &RestLog{deps: deps, tbl: tbl}
}

func (r *RestLog) Title() string   { return "rest log" }
func (r *RestLog) Filtering() bool { return false }
func (r *RestLog) Bindings() []key.Binding {
	return []key.Binding{binding("esc", "esc", "back")}
}

func (r *RestLog) Init() tea.Cmd { return r.Refresh() }

func (r *RestLog) Refresh() tea.Cmd {
	return func() tea.Msg { return restLogMsg{calls: client.RecentCalls()} }
}

func (r *RestLog) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case restLogMsg:
		rows := make([][]uitable.Cell, 0, len(msg.calls))
		for _, c := range msg.calls {
			statusStyle := styles.OKText()
			if len(c.Status) > 0 && c.Status[0] != '2' {
				statusStyle = styles.ErrText()
			}
			rows = append(rows, [][]uitable.Cell{{
				uitable.Styled(c.Time.Format("15:04:05"), styles.Dim()),
				uitable.Plain(c.Method),
				uitable.Plain(c.Path),
				uitable.Styled(c.Status, statusStyle),
				uitable.Plain(fmt.Sprintf("%dms", c.Duration.Milliseconds())),
			}}[0])
		}
		r.tbl.SetRows(rows)
		return r, nil
	case tea.KeyMsg:
		r.tbl.Update(msg)
	}
	return r, nil
}

func (r *RestLog) View(width, height int) string {
	out := titleBar(width, "REST calls", r.tbl.Len(), "every request f9s makes, newest first") + "\n"
	if r.tbl.Len() == 0 {
		return out + emptyState(" No calls recorded yet.")
	}
	r.tbl.SetSize(width, height-1)
	return out + r.tbl.View()
}
