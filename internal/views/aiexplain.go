package views

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ai"
	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type evidenceMsg struct {
	text    string
	mapping map[string]string
	err     error
}

type answerMsg struct {
	text string
	err  error
}

// AIExplain runs the explain pipeline for one job: evidence → (anonymize) →
// provider → de-anonymized diagnosis. Read-only by construction: suggestions
// are text; nothing the model says is ever executed.
type AIExplain struct {
	deps     Deps
	jid      string
	name     string
	provider ai.Provider
	extra    string
	phase    string // gathering / querying / done / error
	evidence string
	shown    string // evidence as sent (possibly anonymized) for display
	answer   string
	pending  string // rendered content awaiting the first View
	vp       viewport.Model
	ready    bool
	preview  bool // hold at the redaction-preview gate before sending
	mapping  map[string]string
	err      string
}

// NewAIExplain builds the explain view; the caller has already checked
// policy. extra is appended to the evidence bundle (e.g. skew analysis).
func NewAIExplain(deps Deps, jid, name string, provider ai.Provider, extra string) *AIExplain {
	return &AIExplain{deps: deps, jid: jid, name: name, provider: provider, phase: "gathering", extra: extra}
}

func (a *AIExplain) Title() string   { return "ai explain" }
func (a *AIExplain) Filtering() bool { return false }
func (a *AIExplain) Bindings() []key.Binding {
	return []key.Binding{
		binding("up", "↑↓/pgup", "scroll"),
		binding("esc", "esc", "back"),
	}
}

func (a *AIExplain) Init() tea.Cmd {
	cl := a.deps.CL
	jid := a.jid
	name := a.name
	anonymize := a.deps.Cfg.Contexts[a.deps.Ctx].AI.AnonymizeOn()
	a.preview = a.deps.Cfg.Contexts[a.deps.Ctx].AI.Preview
	extra := a.extra
	return func() tea.Msg {
		text, err := analyze.BuildEvidence(cl, jid)
		if err != nil {
			return evidenceMsg{err: err}
		}
		if extra != "" {
			text += extra
		}
		mapping := map[string]string{}
		if anonymize {
			text, mapping = analyze.Anonymize(text, []string{name})
		}
		return evidenceMsg{text: text, mapping: mapping}
	}
}

// Refresh is a no-op: an explain run is a one-shot pipeline, not a poll.
func (a *AIExplain) Refresh() tea.Cmd { return nil }

func (a *AIExplain) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case evidenceMsg:
		if msg.err != nil {
			a.phase, a.err = "error", "evidence: "+msg.err.Error()
			return a, nil
		}
		a.shown = msg.text
		a.mapping = msg.mapping
		if a.preview {
			a.phase = "preview"
			a.pending = styles.WarnText().Render("REDACTION PREVIEW — exactly this text would be sent to "+a.provider.Name()) +
				styles.Dim().Render("\n<enter> sends · <esc> aborts\n\n") + a.shown
			if a.ready {
				a.vp.SetContent(a.pending)
				a.vp.GotoTop()
			}
			return a, nil
		}
		return a, a.send()

	case answerMsg:
		if msg.err != nil {
			a.phase, a.err = "error", msg.err.Error()
			return a, nil
		}
		a.phase = "done"
		a.answer = msg.text
		a.setContent()
		return a, nil

	case tea.KeyMsg:
		if a.phase == "preview" && msg.String() == "enter" {
			return a, a.send()
		}
		if a.ready {
			var cmd tea.Cmd
			a.vp, cmd = a.vp.Update(msg)
			return a, cmd
		}
	}
	return a, nil
}

// send dispatches the (possibly previewed) evidence to the provider.
func (a *AIExplain) send() tea.Cmd {
	a.phase = "querying"
	provider := a.provider
	mapping := a.mapping
	text := a.shown
	return func() tea.Msg {
		answer, err := provider.Explain(context.Background(), text)
		if err != nil {
			return answerMsg{err: err}
		}
		return answerMsg{text: analyze.Deanonymize(answer, mapping)}
	}
}

func (a *AIExplain) setContent() {
	var b strings.Builder
	b.WriteString(styles.OKText().Render("DIAGNOSIS ("+a.provider.Name()+")") + "\n\n")
	b.WriteString(a.answer + "\n\n")
	b.WriteString(styles.Dim().Render("— evidence sent (verify the work) ") + "\n\n")
	b.WriteString(styles.Dim().Render(a.shown))
	a.pending = b.String()
	if a.ready {
		a.vp.SetContent(a.pending)
	}
}

func (a *AIExplain) View(width, height int) string {
	head := styles.ViewTitle().Render("AI explain · "+a.name) + "  " +
		styles.Dim().Render(a.provider.Name()) + "\n"
	switch a.phase {
	case "gathering":
		return head + styles.Dim().Render(" gathering evidence from the cluster…")
	case "querying":
		return head + styles.Dim().Render(" evidence collected — waiting for "+a.provider.Name()+"…")
	case "preview":
		if !a.ready {
			a.vp = viewport.New(width, height-1)
			a.ready = true
			a.vp.SetContent(a.pending)
		}
		a.vp.Width = width
		a.vp.Height = height - 1
		return head + a.vp.View()
	case "error":
		return head + styles.ErrText().Render(" ⚠ "+a.err) +
			"\n" + styles.Dim().Render(" <esc> back")
	}
	if !a.ready {
		a.vp = viewport.New(width, height-1)
		a.ready = true
		a.vp.SetContent(a.pending)
	}
	a.vp.Width = width
	a.vp.Height = height - 1
	return head + a.vp.View()
}
