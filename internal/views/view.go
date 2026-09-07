// Package views contains the drill-down views of the f9s UI. Views form a
// stack: Jobs → Job detail → Checkpoints / Exceptions. Each view owns its
// data fetching (issued as async tea.Cmds) and its table rendering.
package views

import (
	"os/exec"
	"runtime"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ai"
	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/plugin"
	"github.com/imankhadiv/f9s/internal/sentinel"
	"github.com/imankhadiv/f9s/internal/watch"
)

// Model is one screen in the view stack.
type Model interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (Model, tea.Cmd)
	View(width, height int) string
	Title() string
	// Refresh returns the cmd that re-fetches this view's data; the root app
	// calls it on every poll tick.
	Refresh() tea.Cmd
	// Filtering reports whether the view is capturing text input, in which
	// case global single-letter keybindings must not fire.
	Filtering() bool
	// Bindings lists the view's keybindings; the footer help bar and the
	// full help overlay are generated from them.
	Bindings() []key.Binding
}

// binding is shorthand for a displayable keybinding.
func binding(keys, display, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(keys), key.WithHelp(display, desc))
}

// Deps carries shared dependencies into view constructors.
type Deps struct {
	CL       *client.Client
	Ctx      string          // active context name (keys the savepoint ledger)
	Cfg      *config.Config  // full config (context switching, fleet view)
	Plugins  []plugin.Plugin // exec plugins active in the jobs view
	Sentinel *sentinel.Sentinel
	Watch    *watch.Store
}

// PushMsg asks the root app to push a new view onto the stack.
type PushMsg struct{ M Model }

// PopMsg asks the root app to pop the current view.
type PopMsg struct{}

func push(m Model) tea.Cmd { return func() tea.Msg { return PushMsg{M: m} } }

// ActionKind enumerates the job actions a view can request.
type ActionKind int

// The job actions. The root app owns prompting and execution.
const (
	ActionSavepoint ActionKind = iota
	ActionStop
	ActionCancel
)

// ActionMsg asks the root app to run a job action (after confirmation).
type ActionMsg struct {
	Kind ActionKind
	JID  string
	Name string
}

func requestAction(kind ActionKind, jid, name string) tea.Cmd {
	return func() tea.Msg { return ActionMsg{Kind: kind, JID: jid, Name: name} }
}

// readOnlyGuard blocks a mutating action on a read-only context.
func readOnlyGuard(deps Deps) tea.Cmd {
	if deps.Cfg != nil && deps.Cfg.Contexts[deps.Ctx].ReadOnly {
		return flashErrV("[RO] this context is read-only — mutating actions are disabled (readOnly: true)")
	}
	return nil
}

// ProposalMsg asks the root app to confirm an agent-proposed mutating
// action; nothing runs until the human confirms in the modal.
type ProposalMsg struct {
	Tool string
	Args map[string]string
	Job  string
}

// RescaleMsg asks the root app to open the FLIP-291 rescale modal for a
// vertex.
type RescaleMsg struct {
	JID     string
	Job     string
	VID     string
	Vertex  string
	Current int
}

// PluginMsg asks the root app to run an exec plugin with a JSON payload.
type PluginMsg struct {
	P       plugin.Plugin
	Payload []byte
}

// SwitchContextMsg asks the root app to switch to another cluster context.
type SwitchContextMsg struct{ Name string }

// FlashMsg asks the root app to show a transient status line.
type FlashMsg struct {
	Text  string
	IsErr bool
}

func flash(text string) tea.Cmd {
	return func() tea.Msg { return FlashMsg{Text: text} }
}

func flashErrV(text string) tea.Cmd {
	return func() tea.Msg { return FlashMsg{Text: text, IsErr: true} }
}

// openInBrowser opens a local file with the OS default handler,
// best-effort.
func openInBrowser(path string) {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	_ = exec.Command(cmd, path).Start()
}

// explainJob gates and launches the AI explain pipeline for a job.
func explainJob(deps Deps, jid, name string) tea.Cmd {
	return explainJobWithExtra(deps, jid, name, "")
}

// investigateJob gates and launches the agentic investigation. With no
// provider configured it opens the in-TUI AI setup form instead, which
// retries this action after saving.
func investigateJob(deps Deps, jid, name string) tea.Cmd {
	aiCfg := deps.Cfg.Contexts[deps.Ctx].AI
	if aiCfg.Policy == "deny" {
		return flash("AI is disabled by policy on context " + deps.Ctx)
	}
	if aiCfg.Provider == "" {
		return push(NewAISetup(deps, func() tea.Msg {
			return investigateJob(deps, jid, name)()
		}))
	}
	if g := tlsAIGuard(deps); g != nil {
		return g
	}
	provider, err := ai.New(aiCfg)
	if err != nil {
		return flash("AI: " + err.Error())
	}
	agent, ok := ai.AsAgent(provider)
	if !ok {
		return flash("provider " + provider.Name() + " does not support agentic investigation")
	}
	return push(NewInvestigate(deps, jid, name, agent, provider.Name()))
}

// restoreArgs de-anonymizes a proposal's arguments before they reach the
// executor. The model works on scrubbed text, so an arg it echoes (a jid, a
// savepoint path) can carry a placeholder; execution needs the real value.
func restoreArgs(sc *analyze.Scrubber, args map[string]string) map[string]string {
	if sc == nil {
		return args
	}
	out := make(map[string]string, len(args))
	for k, v := range args {
		out[k] = sc.Restore(v)
	}
	return out
}

// AskCluster opens a cluster-wide Ask chat (no job scope). It gates policy,
// provider, and capability exactly like the job-scoped path.
func AskCluster(deps Deps) tea.Cmd { return askScope(deps, "", "") }

// AIContexter is an OPTIONAL capability: a view that implements it tells the
// AI panel what it is currently showing, so a question is grounded in the
// operator's on-screen context. Return plain text (human-readable — the panel
// seeds it verbatim and the scrubber anonymizes downstream) plus the entity
// names to anonymize. Views that don't implement it fall back to Title().
type AIContexter interface {
	AIContext() (context string, names []string)
}

// AIContextOf reads the current view's page context for the AI panel: its
// Title() as the "knows:" label, plus the AIContext() block (or a generic
// fallback) and the names to scrub.
func AIContextOf(m Model) (label, context string, names []string) {
	if c, ok := m.(AIContexter); ok {
		if ctx, ns := c.AIContext(); ctx != "" {
			return m.Title(), ctx, ns
		}
	}
	return m.Title(), "The operator is viewing the " + m.Title() + " page.", nil
}

// resolveAIAgent runs the same gate as askScope and returns a chat agent, or a
// human-readable reason it can't (for the panel's placeholder). No push side
// effects — the panel can't push AISetup, so it explains instead.
func resolveAIAgent(deps Deps) (ai.Agent, string, string) {
	aiCfg := deps.Cfg.Contexts[deps.Ctx].AI
	if aiCfg.Policy == "deny" {
		return nil, "", "AI is disabled by policy on context " + deps.Ctx
	}
	if aiCfg.Provider == "" {
		return nil, "", "AI not configured — press a on a job to set it up, or add an ai: block to this context"
	}
	provider, err := ai.New(aiCfg)
	if err != nil {
		return nil, "", "AI: " + err.Error()
	}
	agent, ok := ai.AsAgent(provider)
	if !ok {
		return nil, "", "provider " + provider.Name() + " does not support chat"
	}
	return agent, provider.Name(), ""
}

// askScope gates and opens the free-form Ask chat. Pass jid/name to scope it
// to one job; pass empty jid for a cluster-wide conversation. With no provider
// configured it opens the in-TUI AI setup form, which retries after saving.
func askScope(deps Deps, jid, name string) tea.Cmd {
	aiCfg := deps.Cfg.Contexts[deps.Ctx].AI
	if aiCfg.Policy == "deny" {
		return flash("AI is disabled by policy on context " + deps.Ctx)
	}
	if aiCfg.Provider == "" {
		return push(NewAISetup(deps, func() tea.Msg {
			return askScope(deps, jid, name)()
		}))
	}
	if g := tlsAIGuard(deps); g != nil {
		return g
	}
	provider, err := ai.New(aiCfg)
	if err != nil {
		return flash("AI: " + err.Error())
	}
	agent, ok := ai.AsAgent(provider)
	if !ok {
		return flash("provider " + provider.Name() + " does not support conversational ask")
	}
	return push(NewAskView(deps, jid, name, agent, provider.Name()))
}

// tlsAIAck remembers per-context acceptance of the tlsSkipVerify+AI
// combination for this session.
var tlsAIAck = map[string]bool{}

// tlsAIGuard blocks the first AI call on a context that skips TLS
// verification: evidence read over an unverifiable connection would be
// sent to an AI provider. A second press accepts for the session.
func tlsAIGuard(deps Deps) tea.Cmd {
	cctx := deps.Cfg.Contexts[deps.Ctx]
	if !cctx.TLSSkipVerify || tlsAIAck[deps.Ctx] {
		return nil
	}
	tlsAIAck[deps.Ctx] = true
	return flashErrV("⚠ tlsSkipVerify is ON for this context — cluster evidence from an unverified TLS connection would be sent to the AI provider. Press the key again to accept for this session.")
}

// explainJobWithExtra is explainJob plus extra evidence appended to the
// bundle (e.g. a skew analysis block). With no provider configured it opens
// the in-TUI AI setup form, which retries this action after saving.
func explainJobWithExtra(deps Deps, jid, name, extra string) tea.Cmd {
	aiCfg := deps.Cfg.Contexts[deps.Ctx].AI
	if aiCfg.Policy == "deny" {
		return flash("AI is disabled by policy on context " + deps.Ctx)
	}
	if aiCfg.Provider == "" {
		return push(NewAISetup(deps, func() tea.Msg {
			return explainJobWithExtra(deps, jid, name, extra)()
		}))
	}
	if g := tlsAIGuard(deps); g != nil {
		return g
	}
	provider, err := ai.New(aiCfg)
	if err != nil {
		return flash("AI: " + err.Error())
	}
	return push(NewAIExplain(deps, jid, name, provider, extra))
}
