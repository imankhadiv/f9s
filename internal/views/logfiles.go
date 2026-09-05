package views

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type logFilesMsg struct {
	files []model.LogFile
	err   error
}

// LogFiles lists a JM/TM's rotated log files; enter opens one.
type LogFiles struct {
	deps  Deps
	title string
	list  func() ([]model.LogFile, error)
	open  func(name string) (string, error)
	files []model.LogFile
	tbl   *uitable.Model
	err   string
}

// NewTMLogFiles builds the file picker for one TaskManager.
func NewTMLogFiles(deps Deps, id string) *LogFiles {
	cl := deps.CL
	return newLogFiles(deps, "logs("+id+")",
		func() ([]model.LogFile, error) { return cl.TMLogFiles(id) },
		func(name string) (string, error) { return cl.TMLogFile(id, name) })
}

// NewJMLogFiles builds the file picker for the JobManager.
func NewJMLogFiles(deps Deps) *LogFiles {
	cl := deps.CL
	return newLogFiles(deps, "logs(jobmanager)",
		func() ([]model.LogFile, error) { return cl.JMLogFiles() },
		func(name string) (string, error) { return cl.JMLogFile(name) })
}

func newLogFiles(deps Deps, title string, list func() ([]model.LogFile, error), open func(string) (string, error)) *LogFiles {
	tbl := newTable([]uitable.Column{
		{Title: "FILE", Width: -1},
		{Title: "SIZE", Width: 10},
		{Title: "MODIFIED", Width: 17},
	})
	return &LogFiles{deps: deps, title: title, list: list, open: open, tbl: tbl}
}

func (l *LogFiles) Title() string   { return l.title }
func (l *LogFiles) Filtering() bool { return false }
func (l *LogFiles) Bindings() []key.Binding {
	return []key.Binding{
		binding("enter", "enter", "open file"),
		binding("esc", "esc", "back"),
	}
}

func (l *LogFiles) Init() tea.Cmd { return l.Refresh() }

func (l *LogFiles) Refresh() tea.Cmd {
	list := l.list
	return func() tea.Msg {
		files, err := list()
		return logFilesMsg{files: files, err: err}
	}
}

func (l *LogFiles) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case logFilesMsg:
		if msg.err != nil {
			l.err = msg.err.Error()
			return l, nil
		}
		l.err = ""
		l.files = msg.files
		rows := make([][]uitable.Cell, 0, len(msg.files))
		keys := make([]string, 0, len(msg.files))
		for _, f := range msg.files {
			keys = append(keys, f.Name)
			rows = append(rows, []uitable.Cell{
				uitable.Plain(f.Name),
				uitable.Plain(model.FmtBytes(f.Size)),
				uitable.Styled(model.FmtAge(f.Mtime)+" ago", styles.Dim()),
			})
		}
		l.tbl.SetRowsKeyed(rows, keys)
		return l, nil
	case tea.KeyMsg:
		if msg.String() == "enter" && len(l.files) > 0 {
			f := l.files[l.tbl.Cursor()]
			open := l.open
			name := f.Name
			return l, push(NewTextPager(l.deps, name, func() (string, error) { return open(name) }))
		}
		l.tbl.Update(msg)
	}
	return l, nil
}

func (l *LogFiles) View(width, height int) string {
	out := styles.ViewTitle().Render(fmt.Sprintf("Log files[%d]", l.tbl.Len())) + "\n"
	used := 1
	if l.err != "" {
		out += styles.ErrText().Render("⚠ "+l.err) + "\n"
		used++
	}
	l.tbl.SetSize(width, height-used)
	return out + l.tbl.View()
}
