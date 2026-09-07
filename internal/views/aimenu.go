package views

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ui/styles"
)

// aiOption is one entry in the AI menu — a label, its direct hotkey, and the
// action that opens it (reusing the existing gated helpers).
type aiOption struct {
	hotkey string
	name   string
	desc   string
	run    func(deps Deps, jid, name string) tea.Cmd
}

var aiOptions = []aiOption{
	{"a", "Explain", "one-shot diagnosis from a live evidence bundle", explainJob},
	{"i", "Investigate", "agentic deep-dive — the model drills with read-only tools, every call shown", investigateJob},
	{"k", "Ask", "free-form chat about this job; can propose actions you approve", askScope},
}

// AIMenu is the single AI entry point: one footer key opens this, and the
// operator picks explain / investigate / ask — instead of three separate keys
// cluttering the job view.
type AIMenu struct {
	deps Deps
	jid  string
	name string
	cur  int
}

// NewAIMenu builds the AI chooser for one job.
func NewAIMenu(deps Deps, jid, name string) *AIMenu {
	return &AIMenu{deps: deps, jid: jid, name: name}
}

func (m *AIMenu) Title() string    { return "ai" }
func (m *AIMenu) Filtering() bool  { return false }
func (m *AIMenu) Init() tea.Cmd    { return nil }
func (m *AIMenu) Refresh() tea.Cmd { return nil }
func (m *AIMenu) Bindings() []key.Binding {
	return []key.Binding{
		binding("enter", "enter", "open"),
		binding("up", "↑↓", "move"),
		binding("esc", "esc", "back"),
	}
}

// choose replaces the menu with the selected AI view (pop self, then push).
func (m *AIMenu) choose(i int) tea.Cmd {
	if i < 0 || i >= len(aiOptions) {
		return nil
	}
	act := aiOptions[i].run(m.deps, m.jid, m.name)
	return tea.Batch(func() tea.Msg { return PopMsg{} }, act)
}

func (m *AIMenu) Update(msg tea.Msg) (Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "up", "k":
		if m.cur > 0 {
			m.cur--
		}
	case "down", "j":
		if m.cur < len(aiOptions)-1 {
			m.cur++
		}
	case "enter":
		return m, m.choose(m.cur)
	default:
		s := k.String()
		for i, o := range aiOptions {
			if s == o.hotkey {
				return m, m.choose(i)
			}
		}
		if len(s) == 1 && s >= "1" && s <= "9" {
			return m, m.choose(int(s[0] - '1'))
		}
	}
	return m, nil
}

func (m *AIMenu) View(width, height int) string {
	return framed(width, height, "AI", -1, m.name, func(iw, ih int) string {
		var b strings.Builder
		b.WriteString(" " + styles.Dim().Render("What should the AI do for this job?") + "\n\n")
		for i, o := range aiOptions {
			cursor := "  "
			nameStyle := styles.ViewTitle()
			if i == m.cur {
				cursor = styles.Accent().Render("▸ ")
				nameStyle = styles.Accent().Bold(true)
			}
			b.WriteString(" " + cursor + styles.Dim().Render("["+o.hotkey+"] ") + nameStyle.Render(o.name) + "\n")
			b.WriteString("     " + styles.Dim().Render(o.desc) + "\n\n")
		}
		b.WriteString(" " + styles.Dim().Render("↑↓ move · enter open · a/i/k jump · esc back"))
		return b.String()
	})
}
