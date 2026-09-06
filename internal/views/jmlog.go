package views

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type jmLogMsg struct {
	text string
	err  error
}

// JMLog tails the JobManager log — where job submissions, restarts, and
// checkpoint failures land.
type JMLog struct {
	deps  Deps
	text  string
	vp    viewport.Model
	ready bool
	err   string
}

// NewJMLog builds the JobManager log view.
func NewJMLog(deps Deps) *JMLog {
	return &JMLog{deps: deps}
}

func (l *JMLog) Title() string   { return "jm log" }
func (l *JMLog) Filtering() bool { return false }
func (l *JMLog) Bindings() []key.Binding {
	return []key.Binding{
		binding("up", "↑↓/pgup", "scroll"),
		binding("r", "r", "reload"),
		binding("F", "F", "log files"),
		binding("o", "o", "stdout"),
		binding("esc", "esc", "back"),
	}
}

func (l *JMLog) Init() tea.Cmd { return l.Refresh() }

func (l *JMLog) Refresh() tea.Cmd {
	cl := l.deps.CL
	return func() tea.Msg {
		text, err := cl.JMLog()
		return jmLogMsg{text: text, err: err}
	}
}

func (l *JMLog) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case jmLogMsg:
		if msg.err != nil {
			l.err = msg.err.Error()
			return l, nil
		}
		l.err = ""
		atBottom := !l.ready || l.vp.AtBottom()
		l.text = tail(msg.text, 1000)
		if l.ready {
			l.vp.SetContent(l.text)
			if atBottom {
				l.vp.GotoBottom()
			}
		}
		return l, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "F":
			return l, push(NewJMLogFiles(l.deps))
		case "o":
			cl := l.deps.CL
			return l, push(NewTextPager(l.deps, "stdout(jobmanager)", func() (string, error) { return cl.JMStdout() }))
		}
		if l.ready {
			var cmd tea.Cmd
			l.vp, cmd = l.vp.Update(msg)
			return l, cmd
		}
	}
	return l, nil
}

func (l *JMLog) View(width, height int) string {
	return framed(width, height, "JobManager log", -1, "last 1000 lines · <r> reloads", func(iw, ih int) string {
		out := ""
		used := 0
		if l.err != "" {
			out += styles.ErrText().Render("⚠ "+l.err) + "\n"
			used++
		}
		if !l.ready {
			l.vp = viewport.New(iw, ih-used)
			l.ready = true
			if l.text != "" {
				l.vp.SetContent(l.text)
				l.vp.GotoBottom()
			}
		}
		l.vp.Width = iw
		l.vp.Height = ih - used
		if l.text == "" {
			l.vp.SetContent(styles.Dim().Render("loading JobManager log…"))
		}
		return out + l.vp.View()
	})
}
