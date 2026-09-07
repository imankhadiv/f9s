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
	ex    model.Exceptions // retained for AIContext
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
		e.ex = msg.ex
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

// AIContext summarizes the on-screen exceptions for the AI panel.
func (e *ExceptionsView) AIContext() (string, []string) {
	var b strings.Builder
	fmt.Fprintf(&b, "## On screen: exceptions for %s\n", e.name)
	if e.ex.RootException == "" && len(e.ex.History.Entries) == 0 {
		b.WriteString("no exceptions — job is healthy\n")
		return b.String(), []string{e.name}
	}
	if e.ex.RootException != "" {
		fmt.Fprintf(&b, "root exception (%s):\n", fmtTS(e.ex.Timestamp))
		for i, ln := range strings.Split(e.ex.RootException, "\n") {
			if i >= 10 {
				b.WriteString("  …\n")
				break
			}
			fmt.Fprintf(&b, "  %s\n", ln)
		}
	}
	if len(e.ex.History.Entries) > 0 {
		b.WriteString("history:\n")
		for i, en := range e.ex.History.Entries {
			if i >= 3 {
				break
			}
			fmt.Fprintf(&b, "  #%d %s · %s · task %s\n", i+1, en.ExceptionName, fmtTS(en.Timestamp), en.TaskName)
		}
	}
	return b.String(), []string{e.name}
}

func (e *ExceptionsView) View(width, height int) string {
	return framed(width, height, "Exceptions", -1, e.name, func(iw, ih int) string {
		out := ""
		used := 0
		if e.err != "" {
			out += errorBanner(e.err)
			used++
		}
		if !e.ready {
			e.vp = viewport.New(iw, ih-used)
			e.ready = true
			e.vp.SetContent(emptyState("Loading exceptions…"))
		}
		e.vp.Width = iw
		e.vp.Height = ih - used
		return out + e.vp.View()
	})
}
