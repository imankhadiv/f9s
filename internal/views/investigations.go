package views

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
	"github.com/imankhadiv/f9s/internal/watch"
)

// Investigations lists background watch-mode findings; enter opens one.
type Investigations struct {
	deps    Deps
	tbl     *uitable.Model
	results []watch.Result
	detail  *watch.Result
	vp      viewport.Model
	ready   bool
}

// NewInvestigations builds the findings view.
func NewInvestigations(deps Deps) *Investigations {
	tbl := newTable([]uitable.Column{
		{Title: "WHEN", Width: 8},
		{Title: "JOB", Width: -1},
		{Title: "TRIGGER", Width: 13},
		{Title: "STEPS", Width: 5},
		{Title: "STATUS", Width: 8},
	})
	return &Investigations{deps: deps, tbl: tbl}
}

func (v *Investigations) Title() string   { return "investigations" }
func (v *Investigations) Filtering() bool { return false }
func (v *Investigations) Bindings() []key.Binding {
	if v.detail != nil {
		return []key.Binding{binding("esc", "esc", "back to list")}
	}
	return []key.Binding{binding("enter", "enter", "open"), binding("esc", "esc", "back")}
}

func (v *Investigations) Init() tea.Cmd { return v.Refresh() }

type invRefreshMsg struct{}

func (v *Investigations) Refresh() tea.Cmd {
	return func() tea.Msg { return invRefreshMsg{} }
}

func (v *Investigations) rebuild() {
	if v.deps.Watch == nil {
		return
	}
	v.results = v.deps.Watch.List()
	rows := make([][]uitable.Cell, 0, len(v.results))
	for _, r := range v.results {
		status := uitable.Styled("ok", styles.OKText())
		if r.Err != "" {
			status = uitable.Styled("error", styles.ErrText())
		}
		rows = append(rows, []uitable.Cell{
			uitable.Styled(r.StartedAt.Format("15:04:05"), styles.Dim()),
			uitable.Plain(r.Job),
			uitable.Plain(r.Trigger),
			uitable.Plain(strconv.Itoa(len(r.Steps))),
			status,
		})
	}
	v.tbl.SetRows(rows)
}

func (v *Investigations) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case invRefreshMsg:
		v.rebuild()
		return v, nil
	case tea.KeyMsg:
		if v.detail != nil {
			if msg.String() == "esc" {
				v.detail = nil
				return v, nil
			}
			var cmd tea.Cmd
			v.vp, cmd = v.vp.Update(msg)
			return v, cmd
		}
		if msg.String() == "enter" && len(v.results) > 0 {
			r := v.results[v.tbl.Cursor()]
			v.detail = &r
			v.ready = false
			return v, nil
		}
		v.tbl.Update(msg)
	}
	return v, nil
}

func (v *Investigations) View(width, height int) string {
	if v.detail != nil {
		r := *v.detail
		var b strings.Builder
		b.WriteString(styles.Dim().Render("triggered by "+r.Trigger+" alert at "+r.StartedAt.Format("15:04:05")) + "\n\n")
		b.WriteString(styles.Dim().Render("steps:") + "\n")
		for _, s := range r.Steps {
			b.WriteString(" " + styles.WarnText().Render(s) + "\n")
		}
		if r.Err != "" {
			b.WriteString("\n" + styles.ErrText().Render("⚠ "+r.Err) + "\n")
		}
		if r.Answer != "" {
			b.WriteString("\n" + styles.OKText().Render("DIAGNOSIS") + "\n\n" + r.Answer + "\n")
		}
		if !v.ready {
			v.vp = viewport.New(width, height-1)
			v.ready = true
		}
		v.vp.Width = width
		v.vp.Height = height - 1
		v.vp.SetContent(b.String())
		return styles.ViewTitle().Render("Investigation · "+r.Job) + "\n" + v.vp.View()
	}
	v.rebuild()
	out := styles.ViewTitle().Render("Investigations["+strconv.Itoa(v.tbl.Len())+"]") +
		styles.Dim().Render("  auto-run on alerts (watch mode) · queued, never interrupting") + "\n"
	if v.tbl.Len() == 0 {
		return out + styles.Dim().Render(" None yet. Enable with ai: { watch: true } — alerts then trigger\n background investigations (deduped 30m, max 4/hour).")
	}
	v.tbl.SetSize(width, height-1)
	return out + v.tbl.View()
}
