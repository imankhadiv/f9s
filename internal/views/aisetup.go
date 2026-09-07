package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

var aiProviders = []string{"ollama", "openai", "anthropic"}

// providerDefaults prefills sensible values when the provider changes.
var providerDefaults = map[string]struct{ url, model, tokenEnv string }{
	"ollama":    {url: "http://localhost:11434", model: "qwen3:8b"},
	"openai":    {url: "https://api.openai.com", tokenEnv: "OPENAI_API_KEY"},
	"anthropic": {model: "claude-opus-5", tokenEnv: "ANTHROPIC_API_KEY"},
}

// AISetup is the configure-at-the-moment-of-intent form: pressing an AI key
// with no provider configured lands here; saving writes the config and
// immediately retries the action that brought the user in.
type AISetup struct {
	deps     Deps
	retry    tea.Cmd // the AI action to re-run after saving
	provider int
	fields   [3]textinput.Model // url, model, tokenEnv
	focus    int                // 0=provider, 1..3=fields, 4=save
	err      string
}

// NewAISetup builds the form, prefilled from provider defaults.
func NewAISetup(deps Deps, retry tea.Cmd) *AISetup {
	a := &AISetup{deps: deps, retry: retry}
	labels := []string{"url", "model", "token env var"}
	for i := range a.fields {
		in := textinput.New()
		in.CharLimit = 120
		in.Prompt = ""
		in.Placeholder = labels[i]
		a.fields[i] = in
	}
	a.applyDefaults()
	return a
}

func (a *AISetup) applyDefaults() {
	d := providerDefaults[aiProviders[a.provider]]
	a.fields[0].SetValue(d.url)
	a.fields[1].SetValue(d.model)
	a.fields[2].SetValue(d.tokenEnv)
}

func (a *AISetup) Title() string   { return "ai setup" }
func (a *AISetup) Filtering() bool { return true } // form owns most keys
func (a *AISetup) Bindings() []key.Binding {
	return []key.Binding{
		binding("tab", "tab/↑↓", "next field"),
		binding("left", "←→", "provider"),
		binding("enter", "enter", "save & retry"),
		binding("esc", "esc", "cancel"),
	}
}

func (a *AISetup) Init() tea.Cmd    { return nil }
func (a *AISetup) Refresh() tea.Cmd { return nil }

// save writes the AI block into the context config and returns the retry.
func (a *AISetup) save() (Model, tea.Cmd) {
	cfg := a.deps.Cfg
	cctx := cfg.Contexts[a.deps.Ctx]
	cctx.AI = config.AI{
		Provider: aiProviders[a.provider],
		URL:      strings.TrimSpace(a.fields[0].Value()),
		Model:    strings.TrimSpace(a.fields[1].Value()),
		TokenEnv: strings.TrimSpace(a.fields[2].Value()),
	}
	cfg.Contexts[a.deps.Ctx] = cctx
	if err := config.Save(cfg); err != nil {
		a.err = "save: " + err.Error()
		return a, nil
	}
	// Pop this form FIRST, then flash + retry the pending action. Sequence
	// (not Batch) so the retry's push isn't clobbered by the pop.
	after := []tea.Cmd{flash("AI configured (" + cctx.AI.Provider + ") → " + cfg.Path)}
	if a.retry != nil {
		after = append(after, a.retry)
	}
	return a, tea.Sequence(func() tea.Msg { return PopMsg{} }, tea.Batch(after...))
}

func (a *AISetup) Update(msg tea.Msg) (Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return a, nil
	}
	switch km.String() {
	case "esc":
		return a, func() tea.Msg { return PopMsg{} }
	case "tab", "down":
		a.focus = (a.focus + 1) % 5
	case "shift+tab", "up":
		a.focus = (a.focus + 4) % 5
	case "left", "right":
		if a.focus == 0 {
			if km.String() == "left" {
				a.provider = (a.provider + len(aiProviders) - 1) % len(aiProviders)
			} else {
				a.provider = (a.provider + 1) % len(aiProviders)
			}
			a.applyDefaults()
			return a, nil
		}
	case "enter":
		if a.focus == 4 || a.focus == 0 {
			return a.save()
		}
		a.focus = (a.focus + 1) % 5
	default:
		if a.focus >= 1 && a.focus <= 3 {
			var cmd tea.Cmd
			a.fields[a.focus-1], cmd = a.fields[a.focus-1].Update(msg)
			return a, cmd
		}
	}
	for i := range a.fields {
		if a.focus == i+1 {
			a.fields[i].Focus()
		} else {
			a.fields[i].Blur()
		}
	}
	return a, nil
}

func (a *AISetup) View(width, height int) string {
	var b strings.Builder
	b.WriteString(styles.ViewTitle().Render("AI setup · "+a.deps.Ctx) +
		styles.Dim().Render("  configure once, right here — saved to "+a.deps.Cfg.Path) + "\n\n")

	provs := make([]string, len(aiProviders))
	for i, p := range aiProviders {
		if i == a.provider {
			provs[i] = styles.Header().Render("[" + p + "]")
		} else {
			provs[i] = styles.Dim().Render(" " + p + " ")
		}
	}
	cursor := func(on bool) string {
		if on {
			return styles.Header().Render("❯ ")
		}
		return "  "
	}
	b.WriteString(cursor(a.focus == 0) + fmt.Sprintf("%-14s", "provider") + strings.Join(provs, "") +
		styles.Dim().Render("   ←→ switches") + "\n")
	labels := []string{"url", "model", "token env var"}
	for i, in := range a.fields {
		b.WriteString(cursor(a.focus == i+1) + fmt.Sprintf("%-14s", labels[i]) + in.View() + "\n")
	}
	saveLabel := " save & retry "
	if a.focus == 4 {
		saveLabel = styles.Header().Render("[ save & retry ]")
	} else {
		saveLabel = styles.Dim().Render("[ save & retry ]")
	}
	b.WriteString("\n" + cursor(a.focus == 4) + saveLabel + "\n")
	b.WriteString("\n" + styles.Dim().Render(" secrets stay as environment-variable names — the key itself is never written to disk.\n the token env var is read at request time; leave it empty for local Ollama."))
	if a.err != "" {
		b.WriteString("\n" + styles.ErrText().Render(" ⚠ "+a.err))
	}
	return b.String()
}
