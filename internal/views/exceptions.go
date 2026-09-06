package views

import (
	"fmt"
	"github.com/charmbracelet/bubbles/key"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type exceptionsMsg struct {
	ex  model.Exceptions
	err error
}

// ExceptionsView shows the root exception and exception history of one job.
type ExceptionsView struct {
	deps  Deps
	jid   string
	name  string
	vp    viewport.Model
	ready bool
	err   string
}

// NewExceptions builds the exceptions view for one job.
func NewExceptions(deps Deps, jid, name string) *ExceptionsView {
	return &ExceptionsView{deps: deps, jid: jid, name: name}
}

func (e *ExceptionsView) Title() string   { return "exceptions" }
func (e *ExceptionsView) Filtering() bool { return false }
func (e *ExceptionsView) Bindings() []key.Binding {
	return []key.Binding{binding("up", "↑↓/pgup", "scroll"), binding("esc", "esc", "back")}
}

func (e *ExceptionsView) Init() tea.Cmd { return e.Refresh() }

func (e *ExceptionsView) Refresh() tea.Cmd {
	cl := e.deps.CL
	jid := e.jid
	return func() tea.Msg {
		ex, err := cl.Exceptions(jid)
		return exceptionsMsg{ex: ex, err: err}
	}
}

func (e *ExceptionsView) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case exceptionsMsg:
		if msg.err != nil {
			e.err = msg.err.Error()
			return e, nil
		}
		e.err = ""
		e.vp.SetContent(e.render(msg.ex))
		return e, nil
	}
	var cmd tea.Cmd
	e.vp, cmd = e.vp.Update(msg)
	return e, cmd
}

func (e *ExceptionsView) render(ex model.Exceptions) string {
	var b strings.Builder
	if ex.RootException == "" && len(ex.History.Entries) == 0 {
		return emptyState("No exceptions. This job is having a good day.")
	}
	if ex.RootException != "" {
		b.WriteString(styles.ErrText().Render("ROOT EXCEPTION") + "  " +
			styles.Dim().Render(fmtTS(ex.Timestamp)) + "\n")
		b.WriteString(ex.RootException + "\n\n")
	}
	for i, en := range ex.History.Entries {
		b.WriteString(styles.ViewTitle().Render(fmt.Sprintf("#%d %s", i+1, en.ExceptionName)))
		for k, v := range en.FailureLabels {
			b.WriteString("  " + styles.WarnText().Render("["+k+"="+v+"]"))
		}
		b.WriteString("\n")
		b.WriteString(styles.Dim().Render(fmtTS(en.Timestamp)+" · "+en.TaskName+" @ "+en.Location) + "\n")
		b.WriteString(en.Stacktrace + "\n\n")
	}
	if ex.History.Truncated {
		b.WriteString(styles.Dim().Render("(history truncated by the server)") + "\n")
	}
	return b.String()
}

func fmtTS(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
}

func (e *ExceptionsView) View(width, height int) string {
	if !e.ready {
		e.vp = viewport.New(width, height-2)
		e.ready = true
		e.vp.SetContent(emptyState("Loading exceptions…"))
	}
	e.vp.Width = width
	e.vp.Height = height - 2
	out := titleBar(width, "Exceptions", -1, e.name) + "\n"
	if e.err != "" {
		out += errorBanner(e.err)
	}
	return out + e.vp.View()
}
