package views

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type textPagerMsg struct {
	text string
	err  error
}

// TextPager renders any lazily-fetched text (a log file, stdout) in a
// scrollable viewport with reload.
type TextPager struct {
	deps  Deps
	title string
	fetch func() (string, error)
	text  string
	vp    viewport.Model
	ready bool
	err   string
}

// NewTextPager builds a pager over fetch's result.
func NewTextPager(deps Deps, title string, fetch func() (string, error)) *TextPager {
	return &TextPager{deps: deps, title: title, fetch: fetch}
}

func (p *TextPager) Title() string   { return p.title }
func (p *TextPager) Filtering() bool { return false }
func (p *TextPager) Bindings() []key.Binding {
	return []key.Binding{
		binding("up", "↑↓/pgup", "scroll"),
		binding("r", "r", "reload"),
		binding("esc", "esc", "back"),
	}
}

func (p *TextPager) Init() tea.Cmd { return p.Refresh() }

func (p *TextPager) Refresh() tea.Cmd {
	fetch := p.fetch
	return func() tea.Msg {
		text, err := fetch()
		return textPagerMsg{text: text, err: err}
	}
}

func (p *TextPager) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case textPagerMsg:
		if msg.err != nil {
			p.err = msg.err.Error()
			return p, nil
		}
		p.err = ""
		atBottom := !p.ready || p.vp.AtBottom()
		p.text = tail(msg.text, 2000)
		if p.ready {
			p.vp.SetContent(p.text)
			if atBottom {
				p.vp.GotoBottom()
			}
		}
		return p, nil
	case tea.KeyMsg:
		if p.ready {
			var cmd tea.Cmd
			p.vp, cmd = p.vp.Update(msg)
			return p, cmd
		}
	}
	return p, nil
}

func (p *TextPager) View(width, height int) string {
	head := styles.ViewTitle().Render(p.title) +
		styles.Dim().Render("  · last 2000 lines · <r> reloads") + "\n"
	used := 1
	if p.err != "" {
		head += styles.ErrText().Render("⚠ "+p.err) + "\n"
		used++
	}
	if !p.ready {
		p.vp = viewport.New(width, height-used)
		p.ready = true
		if p.text != "" {
			p.vp.SetContent(p.text)
			p.vp.GotoBottom()
		}
	}
	p.vp.Width = width
	p.vp.Height = height - used
	if p.text == "" && p.err == "" {
		p.vp.SetContent(styles.Dim().Render("loading…"))
	}
	return head + p.vp.View()
}
