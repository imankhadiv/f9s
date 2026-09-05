package views

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/sqlgw"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

const sqlMaxRows = 200

type gwSessionMsg struct {
	session string
	err     error
}

type gwResultMsg struct {
	stmt       string
	cols       []string
	rows       [][]string
	newSession string // set when the session was transparently reopened
	err        error
}

// SQLConsole is a REPL against the Flink SQL Gateway. Streaming SELECTs are
// capped at sqlMaxRows — the console is for inspection, not consumption.
type SQLConsole struct {
	deps    Deps
	gw      *sqlgw.Client
	session string
	input   textinput.Model
	tbl     *uitable.Model
	focused bool
	running bool
	status  string
	history []string
	histPos int // len(history) = editing a fresh line
	err     string
}

// NewSQLConsole builds the console for a context's configured gateway.
func NewSQLConsole(deps Deps, url string) *SQLConsole {
	in := textinput.New()
	in.Prompt = "sql> "
	in.CharLimit = 500
	in.Focus()
	history := loadSQLHistory(deps.Ctx)
	return &SQLConsole{
		deps:    deps,
		gw:      sqlgw.New(url),
		input:   in,
		tbl:     newTable([]uitable.Column{{Title: "RESULT", Width: -1}}),
		focused: true,
		status:  "opening session…",
		history: history,
		histPos: len(history),
	}
}

func (s *SQLConsole) Title() string   { return "sql" }
func (s *SQLConsole) Filtering() bool { return s.focused }
func (s *SQLConsole) Bindings() []key.Binding {
	return []key.Binding{
		binding("enter", "enter", "run statement"),
		binding("up", "↑↓", "history"),
		binding("esc", "esc", "results mode / back"),
		binding("i", "i", "edit statement"),
	}
}

func (s *SQLConsole) Init() tea.Cmd {
	gw := s.gw
	return func() tea.Msg {
		session, err := gw.OpenSession()
		return gwSessionMsg{session: session, err: err}
	}
}

// Refresh is a no-op: the console only talks to the gateway on enter.
func (s *SQLConsole) Refresh() tea.Cmd { return nil }

func (s *SQLConsole) run(stmt string) tea.Cmd {
	gw := s.gw
	session := s.session
	ctx := s.deps.Ctx
	return func() tea.Msg {
		appendSQLHistory(ctx, stmt)
		exec := func(sess string) (string, error) { return gw.Execute(sess, stmt) }
		op, err := exec(session)
		if err != nil && sessionExpired(err) {
			// the gateway idle-timed our session — reopen and retry once
			if fresh, oerr := gw.OpenSession(); oerr == nil {
				session = fresh
				op, err = exec(fresh)
				if err == nil {
					cols, rows, ferr := gw.FetchAll(fresh, op, sqlMaxRows, 30*time.Second)
					return gwResultMsg{stmt: stmt, cols: cols, rows: rows, err: ferr, newSession: fresh}
				}
			}
		}
		if err != nil {
			return gwResultMsg{stmt: stmt, err: err}
		}
		cols, rows, err := gw.FetchAll(session, op, sqlMaxRows, 30*time.Second)
		return gwResultMsg{stmt: stmt, cols: cols, rows: rows, err: err}
	}
}

// sessionExpired recognizes the gateway's unknown/expired-session errors.
func sessionExpired(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "session") &&
		(strings.Contains(msg, "not exist") || strings.Contains(msg, "expired") || strings.Contains(msg, "does not"))
}

func (s *SQLConsole) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case gwSessionMsg:
		if msg.err != nil {
			s.err = "gateway: " + msg.err.Error()
			s.status = ""
			return s, nil
		}
		s.session = msg.session
		s.status = "session open — type a statement and press enter"
		return s, nil

	case gwResultMsg:
		s.running = false
		if msg.newSession != "" {
			s.session = msg.newSession
		}
		if msg.stmt != "" && (len(s.history) == 0 || s.history[len(s.history)-1] != msg.stmt) {
			s.history = append(s.history, msg.stmt)
		}
		s.histPos = len(s.history)
		if msg.err != nil {
			s.err = msg.err.Error()
			s.status = ""
			return s, nil
		}
		s.err = ""
		s.status = statusFor(msg)
		cols := make([]uitable.Column, len(msg.cols))
		for i, c := range msg.cols {
			w := 24
			if i == len(msg.cols)-1 {
				w = -1
			}
			cols[i] = uitable.Column{Title: c, Width: w}
		}
		if len(cols) == 0 {
			cols = []uitable.Column{{Title: "RESULT", Width: -1}}
		}
		s.tbl.SetColumns(cols)
		rows := make([][]uitable.Cell, len(msg.rows))
		for i, r := range msg.rows {
			cells := make([]uitable.Cell, len(r))
			for j, v := range r {
				cells[j] = uitable.Plain(v)
			}
			rows[i] = cells
		}
		s.tbl.SetRows(rows)
		return s, nil

	case tea.KeyMsg:
		if s.focused {
			switch msg.String() {
			case "enter":
				stmt := s.input.Value()
				if stmt == "" || s.running || s.session == "" {
					return s, nil
				}
				s.running = true
				s.status = "running…"
				s.err = ""
				return s, s.run(stmt)
			case "esc":
				s.focused = false
				s.input.Blur()
				return s, nil
			case "up":
				if s.histPos > 0 {
					s.histPos--
					s.input.SetValue(s.history[s.histPos])
					s.input.CursorEnd()
				}
				return s, nil
			case "down":
				if s.histPos < len(s.history) {
					s.histPos++
					if s.histPos == len(s.history) {
						s.input.SetValue("")
					} else {
						s.input.SetValue(s.history[s.histPos])
						s.input.CursorEnd()
					}
				}
				return s, nil
			}
			var cmd tea.Cmd
			s.input, cmd = s.input.Update(msg)
			return s, cmd
		}
		switch msg.String() {
		case "i", "/":
			s.focused = true
			return s, s.input.Focus()
		default:
			s.tbl.Update(msg)
		}
	}
	return s, nil
}

func statusFor(msg gwResultMsg) string {
	n := len(msg.rows)
	suffix := ""
	if n >= sqlMaxRows {
		suffix = " (capped — streaming query still running on the cluster)"
	}
	return msg.stmt + " → " + strconv.Itoa(n) + " rows" + suffix
}

func (s *SQLConsole) View(width, height int) string {
	out := styles.ViewTitle().Render("SQL Gateway") + "  " + styles.Dim().Render(s.deps.Ctx) + "\n"
	out += s.input.View() + "\n"
	used := 2
	if s.status != "" {
		out += styles.Dim().Render(" "+s.status) + "\n"
		used++
	}
	if s.err != "" {
		out += styles.ErrText().Render(" ⚠ "+firstLine(s.err)) + "\n"
		used++
	}
	s.tbl.SetSize(width, height-used)
	return out + s.tbl.View()
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i] + " …"
		}
	}
	return s
}
