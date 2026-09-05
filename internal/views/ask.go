package views

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ai"
	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/incident"
	"github.com/imankhadiv/f9s/internal/invlog"
	"github.com/imankhadiv/f9s/internal/runbook"
	"github.com/imankhadiv/f9s/internal/tools"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

// qaTurn is one line of the transcript.
type qaTurn struct {
	who  string // "you" / "ai" / "err"
	text string
}

// AskView is a free-form, multi-turn conversation with the AI, grounded in
// either one job's evidence bundle (job scope) or a cluster snapshot. Each
// question runs an agentic tool loop; the loop is stateless, so the view
// replays the transcript as context on every turn.
type AskView struct {
	deps     Deps
	jid      string // "" for cluster-wide
	name     string // job name, or "cluster"
	agent    ai.Agent
	provider string

	input       textinput.Model
	turns       []qaTurn
	steps       []string // live tool-calls for the in-flight question
	props       []ai.Proposal
	suggestions []string // starter questions shown on an empty chat
	busy        bool
	events      chan agentEvent

	// grounding, resolved lazily on the first question and then reused.
	evidence string
	grounded bool
	scrubber *analyze.Scrubber
	anon     bool

	vp    viewport.Model
	ready bool
}

// NewAskView builds the chat. Pass jid/name to scope it to one job; pass
// empty jid for a cluster-wide conversation.
func NewAskView(deps Deps, jid, name string, agent ai.Agent, providerName string) *AskView {
	in := textinput.New()
	in.Placeholder = "ask about " + scopeLabel(jid, name) + " — e.g. \"why is the checkpoint slow?\""
	in.Focus()
	in.CharLimit = 500
	if name == "" {
		name = "cluster"
	}
	return &AskView{
		deps: deps, jid: jid, name: name, agent: agent, provider: providerName,
		input: in, anon: deps.Cfg.Contexts[deps.Ctx].AI.AnonymizeOn(),
		suggestions: starterQuestions(jid),
	}
}

// starterQuestions returns a few tappable prompts for an empty chat, tuned to
// the scope — one job's health, or the whole cluster.
func starterQuestions(jid string) []string {
	if jid != "" {
		return []string{
			"Why is this job backpressured?",
			"Is checkpointing healthy?",
			"Should I rescale — and to what?",
			"What's the biggest risk right now?",
		}
	}
	return []string{
		"Which jobs are unhealthy?",
		"Which job wastes the most slots?",
		"Are any jobs failed or restarting?",
		"Where is backpressure worst?",
	}
}

func scopeLabel(jid, name string) string {
	if jid == "" {
		return "the cluster"
	}
	return name
}

func (v *AskView) Title() string { return "ask ai" }

// Filtering keeps global single-key shortcuts from firing while the operator
// is typing a question.
func (v *AskView) Filtering() bool { return true }

func (v *AskView) Bindings() []key.Binding {
	return []key.Binding{
		binding("enter", "enter", "send"),
		binding("pgup", "pgup/pgdn", "scroll"),
		binding("esc", "esc", "back"),
	}
}

func (v *AskView) Init() tea.Cmd { return textinput.Blink }

// Refresh is a no-op: a conversation is driven by the operator, not a timer.
func (v *AskView) Refresh() tea.Cmd { return nil }

func (v *AskView) askListen() tea.Cmd {
	ch := v.events
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return agentEventMsg(agentEvent{kind: "done"})
		}
		return agentEventMsg(ev)
	}
}

// ask starts the agentic loop for one question in a goroutine.
func (v *AskView) ask(question string) tea.Cmd {
	v.events = make(chan agentEvent, 16)
	ch := v.events
	deps := v.deps
	jid, name := v.jid, v.name
	agent := v.agent
	history := v.transcriptText()

	go func() {
		defer close(ch)
		cl := deps.CL
		ctxName := deps.Ctx
		reg := tools.New(cl, ctxName)

		// Resolve grounding once, then reuse it for later questions.
		if !v.grounded {
			ev, names := v.buildGrounding(cl)
			v.evidence = ev
			v.scrubber = analyze.NewScrubber(names)
			v.grounded = true
		}

		var seed strings.Builder
		if v.evidence != "" {
			seed.WriteString(v.evidence)
			seed.WriteString("\n\n")
		}
		if history != "" {
			seed.WriteString("## Conversation so far\n")
			seed.WriteString(history)
			seed.WriteString("\n")
		}
		seed.WriteString("## Current question\n")
		seed.WriteString(question)

		scrub := func(s string) string { return s }
		text := seed.String()
		if v.anon {
			text = v.scrubber.Scrub(text)
			scrub = v.scrubber.Scrub
		}

		lg := invlog.New(ctxName, jidOrCluster(jid), name, "ask")
		lg.Line("Q: " + question)
		answer, err := agent.Ask(context.Background(), text, reg, scrub,
			func(line string) {
				lg.Line(line)
				ch <- agentEvent{kind: "step", text: line}
			},
			func(p ai.Proposal) {
				lg.Line(fmt.Sprintf("PROPOSAL: %s %v", p.Tool, p.Args))
				ch <- agentEvent{kind: "propose", proposal: p}
			})
		if err != nil {
			lg.Close("", err.Error())
			ch <- agentEvent{kind: "err", text: err.Error()}
			return
		}
		if v.anon {
			answer = v.scrubber.Restore(answer)
		}
		lg.Close(answer, "")
		ch <- agentEvent{kind: "done", text: answer}
	}()
	return v.askListen()
}

func jidOrCluster(jid string) string {
	if jid == "" {
		return "cluster"
	}
	return jid
}

// buildGrounding returns the seed evidence and the names to anonymize. Job
// scope uses the full evidence bundle (plus runbook/incident context);
// cluster scope uses a compact job inventory the model can drill into.
func (v *AskView) buildGrounding(cl *client.Client) (string, []string) {
	if v.jid != "" {
		ev, err := analyze.BuildEvidence(cl, v.jid)
		if err != nil {
			return "(could not load the job's evidence bundle: " + err.Error() + " — use your tools instead)", []string{v.name}
		}
		ev = runbook.Inject(ev, v.name, "")
		ev = incident.Inject(v.deps.Ctx, ev, v.name, "")
		return ev, []string{v.name}
	}

	// Cluster scope: a short inventory so the model knows what exists.
	jobs, err := cl.Jobs()
	if err != nil {
		return "(could not list jobs: " + err.Error() + " — use your tools to inspect the cluster)", nil
	}
	var b strings.Builder
	b.WriteString("## Cluster job inventory\n")
	names := make([]string, 0, len(jobs))
	for _, j := range jobs {
		b.WriteString(fmt.Sprintf("- %s  [%s]  jid=%s\n", j.Name, j.State, j.JID))
		names = append(names, j.Name)
	}
	if len(jobs) == 0 {
		b.WriteString("(no jobs)\n")
	}
	return b.String(), names
}

// transcriptText renders answered turns as plain text for the model.
func (v *AskView) transcriptText() string {
	var b strings.Builder
	for _, t := range v.turns {
		switch t.who {
		case "you":
			b.WriteString("You: " + t.text + "\n")
		case "ai":
			b.WriteString("Assistant: " + t.text + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func (v *AskView) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case agentEventMsg:
		switch msg.kind {
		case "step":
			v.steps = append(v.steps, msg.text)
			v.syncContent()
			return v, v.askListen()
		case "propose":
			v.props = append(v.props, msg.proposal)
			v.syncContent()
			return v, v.askListen()
		case "err":
			v.turns = append(v.turns, qaTurn{who: "err", text: msg.text})
			v.busy = false
			v.steps = nil
			v.syncContent()
			return v, v.input.Focus()
		case "done":
			if msg.text != "" {
				v.turns = append(v.turns, qaTurn{who: "ai", text: msg.text})
			}
			v.busy = false
			v.steps = nil
			v.syncContent()
			return v, v.input.Focus()
		}

	case tea.KeyMsg:
		s := msg.String()
		switch s {
		case "enter":
			if !v.busy {
				return v, v.send(strings.TrimSpace(v.input.Value()))
			}
			return v, nil
		case "pgup", "pgdown":
			if v.ready {
				var cmd tea.Cmd
				v.vp, cmd = v.vp.Update(msg)
				return v, cmd
			}
			return v, nil
		}
		// A bare digit is a selection ONLY when the operator isn't mid-question
		// (empty input) and nothing is in flight — otherwise it's typed text.
		// Pending proposals take precedence; on a fresh chat it picks a starter.
		if len(s) == 1 && s >= "1" && s <= "9" && !v.busy && strings.TrimSpace(v.input.Value()) == "" {
			n := int(s[0] - '1')
			if len(v.props) > 0 {
				if n < len(v.props) {
					p := v.props[n]
					job := v.name
					return v, func() tea.Msg { return ProposalMsg{Tool: p.Tool, Args: p.Args, Job: job} }
				}
				return v, nil
			}
			if len(v.turns) == 0 && n < len(v.suggestions) {
				return v, v.send(v.suggestions[n])
			}
		}
		// While a question is in flight the input is frozen; otherwise the
		// keystroke is part of the question being typed.
		if !v.busy {
			var cmd tea.Cmd
			v.input, cmd = v.input.Update(msg)
			return v, cmd
		}
	}
	return v, nil
}

// send submits a question and starts the agentic loop; empty is a no-op.
func (v *AskView) send(q string) tea.Cmd {
	if q == "" {
		return nil
	}
	v.turns = append(v.turns, qaTurn{who: "you", text: q})
	v.input.SetValue("")
	v.busy = true
	v.steps = nil
	v.syncContent()
	return v.ask(q)
}

func (v *AskView) content() string {
	var b strings.Builder
	if len(v.turns) == 0 && !v.busy {
		b.WriteString(styles.Dim().Render("Ask anything about "+scopeLabel(v.jid, v.name)+". The assistant can read the cluster with its tools to answer.") + "\n")
		if len(v.suggestions) > 0 {
			b.WriteString("\n" + styles.Dim().Render("Try one (press the number):") + "\n")
			for i, s := range v.suggestions {
				b.WriteString(fmt.Sprintf(" %s %s\n", styles.OKText().Render(fmt.Sprintf("[%d]", i+1)), s))
			}
		}
	}
	for _, t := range v.turns {
		switch t.who {
		case "you":
			b.WriteString("\n" + styles.ViewTitle().Render("You") + "  " + t.text + "\n")
		case "ai":
			b.WriteString("\n" + styles.OKText().Render("AI ("+v.provider+")") + "\n" + t.text + "\n")
		case "err":
			b.WriteString("\n" + styles.ErrText().Render("⚠ "+t.text) + "\n")
		}
	}
	if v.busy {
		for _, s := range v.steps {
			b.WriteString(" " + styles.WarnText().Render(s) + "\n")
		}
		b.WriteString(styles.Dim().Render(" thinking…") + "\n")
	}
	if len(v.props) > 0 {
		b.WriteString("\n" + styles.ErrText().Render("PROPOSED ACTIONS — press the number to review & approve (nothing runs without confirmation)") + "\n")
		for i, p := range v.props {
			b.WriteString(fmt.Sprintf(" [%d] %s %v\n", i+1, p.Tool, p.Args))
		}
	}
	return b.String()
}

func (v *AskView) syncContent() {
	if v.ready {
		v.vp.SetContent(v.content())
		v.vp.GotoBottom()
	}
}

func (v *AskView) View(width, height int) string {
	head := styles.ViewTitle().Render("Ask AI · "+scopeLabel(v.jid, v.name)) + "  " +
		styles.Dim().Render(v.provider+" · read-only tools") + "\n"
	// Reserve the head line and the input line; the transcript scrolls between.
	bodyH := height - 3
	if bodyH < 1 {
		bodyH = 1
	}
	if !v.ready {
		v.vp = viewport.New(width, bodyH)
		v.ready = true
	}
	v.vp.Width = width
	v.vp.Height = bodyH
	v.vp.SetContent(v.content())

	prompt := "> " + v.input.View()
	if v.busy {
		prompt = styles.Dim().Render("(waiting for the answer — press esc to leave)")
	}
	return head + v.vp.View() + "\n" + prompt
}
