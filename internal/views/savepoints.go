package views

import (
	"os"

	"github.com/aymanbagabas/go-osc52/v2"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ledger"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type ledgerMsg struct {
	entries []ledger.Entry
	err     error
}

// Savepoints is the savepoint ledger view: every savepoint f9s has taken on
// this context, newest first.
type Savepoints struct {
	deps    Deps
	tbl     *uitable.Model
	entries []ledger.Entry
	showCmd bool
	filter  viewFilter
	err     string
}

// NewSavepoints builds the ledger view for the active context.
func NewSavepoints(deps Deps) *Savepoints {
	tbl := newTable([]uitable.Column{
		{Title: "TIME", Width: 17},
		{Title: "JOB", Width: 26},
		{Title: "ACTION", Width: 10},
		{Title: "PATH", Width: -1},
	})
	return &Savepoints{deps: deps, tbl: tbl, filter: newViewFilter()}
}

func (s *Savepoints) Title() string   { return "ledger" }
func (s *Savepoints) Filtering() bool { return s.filter.Active() }
func (s *Savepoints) Bindings() []key.Binding {
	return []key.Binding{
		binding("enter", "enter", "restore cmd"),
		binding("y", "y", "copy path"),
		binding("/", "/", "filter"),
		binding("esc", "esc", "back"),
	}
}

func (s *Savepoints) Init() tea.Cmd { return s.Refresh() }

func (s *Savepoints) Refresh() tea.Cmd {
	ctx := s.deps.Ctx
	return func() tea.Msg {
		entries, err := ledger.List(ctx)
		return ledgerMsg{entries: entries, err: err}
	}
}

func (s *Savepoints) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ledgerMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, nil
		}
		s.err = ""
		s.entries = msg.entries
		rows := make([][]uitable.Cell, 0, len(msg.entries))
		for _, e := range msg.entries {
			rows = append(rows, []uitable.Cell{
				uitable.Styled(e.Time.Format("2006-01-02 15:04"), styles.Dim()),
				uitable.Plain(e.Job),
				uitable.Plain(e.Action),
				uitable.Plain(e.Path),
			})
		}
		s.tbl.SetRows(rows)
		return s, nil
	case tea.KeyMsg:
		if handled, cmd := s.filter.Handle(msg, s.tbl); handled {
			return s, cmd
		}
		switch msg.String() {
		case "enter":
			s.showCmd = !s.showCmd
			return s, nil
		case "y":
			if e, ok := s.selectedEntry(); ok {
				// OSC52 writes through the tty even under the alt screen.
				_, _ = osc52.New(e.Path).WriteTo(os.Stderr)
				return s, flash("savepoint path copied to clipboard")
			}
		default:
			s.tbl.Update(msg)
		}
	}
	return s, nil
}

func (s *Savepoints) selectedEntry() (ledger.Entry, bool) {
	if len(s.entries) == 0 {
		return ledger.Entry{}, false
	}
	return s.entries[s.tbl.Cursor()], true
}

func (s *Savepoints) View(width, height int) string {
	out := titleBar(width, "Savepoint ledger", s.tbl.Len(), s.deps.Ctx) + "\n"
	if s.tbl.Len() == 0 {
		if s.err != "" {
			return out + errorBanner(s.err)
		}
		return out + emptyState("No savepoints recorded yet. Trigger one with <s> on a job —\nf9s remembers every path so you don't have to.")
	}
	used := 1
	if fl := s.filter.Line(); fl != "" {
		out += fl
		used++
	}
	if s.err != "" {
		out += errorBanner(s.err)
		used++
	}
	if s.showCmd {
		if e, ok := s.selectedEntry(); ok {
			out += styles.Dim().Render(" restore: ") +
				"flink run -s " + e.Path + " <your-job.jar>" +
				styles.Dim().Render("   (<y> copies the path)") + "\n"
			used++
		}
	}
	s.tbl.SetSize(width, height-used)
	return out + s.tbl.View()
}
