package views

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/ai"
	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/invlog"
	"github.com/imankhadiv/f9s/internal/tools"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

// PageContext is what the host hands the panel at ask time: the current view's
// "knows:" label, its on-screen context block, and the names to anonymize.
type PageContext struct {
	Label   string
	Context string
	Names   []string
}

// aiPanelEventMsg wraps an agentEvent with the generation it belongs to, so a
// context switch or a stale in-flight answer is discarded rather than shown.
type aiPanelEventMsg struct {
	gen int
	ev  agentEvent
}

// AIPanel is the persistent, page-aware AI chat that lives beside the active
// view (not on the stack) — it survives navigation and re-grounds to whatever
// page the operator is on. It reuses the same agentic Ask loop, scrubber, and
// proposal path as the full Ask view.
type AIPanel struct {
	deps     Deps
	provider func() PageContext // supplied by the host; read at ask time

	agent     ai.Agent
	agentName string
	resolved  bool
	setupErr  string

	input    textinput.Model
	turns    []qaTurn
	steps    []string
	props    []ai.Proposal
	busy     bool
	events   chan agentEvent
	gen      int // bumps on reset; stale events are dropped
	label    string
	scrubber *analyze.Scrubber
	anon     bool

	vp    viewport.Model
	wrapW int
	ready bool
}

// NewAIPanel builds the panel for a context.
func NewAIPanel(deps Deps) *AIPanel {
	in := textinput.New()
	in.Placeholder = "ask about this page…"
	in.CharLimit = 500
	return &AIPanel{
		deps:     deps,
		anon:     deps.Cfg != nil && deps.Cfg.Contexts[deps.Ctx].AI.AnonymizeOn(),
		scrubber: analyze.NewScrubber(nil),
		input:    in,
	}
}

// SetProvider wires the closure the panel calls to learn the current page.
func (p *AIPanel) SetProvider(fn func() PageContext) { p.provider = fn }

// Focus gives the panel's input the cursor.
func (p *AIPanel) Focus() tea.Cmd { return p.input.Focus() }

// Blur takes the cursor away.
func (p *AIPanel) Blur() { p.input.Blur() }

func (p *AIPanel) ensureAgent() {
	if p.resolved {
		return
	}
	p.resolved = true
	p.agent, p.agentName, p.setupErr = resolveAIAgent(p.deps)
}

func (p *AIPanel) listen() tea.Cmd {
	ch, gen := p.events, p.gen
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return aiPanelEventMsg{gen: gen, ev: agentEvent{kind: "done"}}
		}
		return aiPanelEventMsg{gen: gen, ev: ev}
	}
}

// send submits a question grounded in the current page.
func (p *AIPanel) send(question string) tea.Cmd {
	question = strings.TrimSpace(question)
	if question == "" || p.busy {
		return nil
	}
	p.ensureAgent()
	if p.agent == nil {
		p.turns = append(p.turns, qaTurn{who: "err", text: p.setupErr})
		p.syncContent()
		return nil
	}
	pc := PageContext{}
	if p.provider != nil {
		pc = p.provider()
	}
	p.label = pc.Label
	p.turns = append(p.turns, qaTurn{who: "you", text: question})
	p.input.SetValue("")
	p.busy = true
	p.steps = nil

	p.events = make(chan agentEvent, 16)
	ch, gen := p.events, p.gen
	deps, agent := p.deps, p.agent
	history := p.transcriptText()
	for _, n := range pc.Names {
		p.scrubber.AddName(n)
	}
	anon := p.anon
	sc := p.scrubber

	go func() {
		defer close(ch)
		reg := tools.New(deps.CL, deps.Ctx)
		var seed strings.Builder
		seed.WriteString("## Where the operator is\n")
		if pc.Label != "" {
			seed.WriteString("The operator is on the " + pc.Label + " page. On screen:\n")
		}
		seed.WriteString(pc.Context + "\n\n")
		if history != "" {
			seed.WriteString("## Conversation so far\n" + history + "\n\n")
		}
		seed.WriteString("## Current question\n" + question)

		text := seed.String()
		scrub := func(s string) string { return s }
		if anon {
			text = sc.Scrub(text)
			scrub = sc.Scrub
		}
		lg := invlog.New(deps.Ctx, "panel", pc.Label, "panel")
		lg.Line("Q: " + question)
		answer, err := agent.Ask(context.Background(), text, reg, scrub,
			func(line string) { lg.Line(line); ch <- agentEvent{kind: "step", text: line} },
			func(pr ai.Proposal) {
				if anon {
					pr.Args = restoreArgs(sc, pr.Args)
				}
				ch <- agentEvent{kind: "propose", proposal: pr}
			})
		if err != nil {
			lg.Close("", err.Error())
			ch <- agentEvent{kind: "err", text: err.Error()}
			return
		}
		if anon {
			answer = sc.Restore(answer)
		}
		lg.Close(answer, "")
		ch <- agentEvent{kind: "done", text: answer}
	}()
	_ = gen
	p.syncContent()
	return p.listen()
}

// Reset clears the conversation and bumps the generation (on context switch),
// so any in-flight answer from the old context is discarded.
func (p *AIPanel) Reset(deps Deps) {
	g := p.gen + 1
	fn := p.provider
	*p = *NewAIPanel(deps)
	p.gen = g
	p.provider = fn
}

func (p *AIPanel) Update(msg tea.Msg) (*AIPanel, tea.Cmd) {
	switch msg := msg.(type) {
	case aiPanelEventMsg:
		if msg.gen != p.gen {
			return p, nil // stale (context switched) — drop it
		}
		switch msg.ev.kind {
		case "step":
			p.steps = append(p.steps, msg.ev.text)
			p.syncContent()
			return p, p.listen()
		case "propose":
			p.props = append(p.props, msg.ev.proposal)
			p.syncContent()
			return p, p.listen()
		case "err":
			p.turns = append(p.turns, qaTurn{who: "err", text: msg.ev.text})
			p.busy, p.steps = false, nil
			p.syncContent()
			return p, nil
		case "done":
			if msg.ev.text != "" {
				p.turns = append(p.turns, qaTurn{who: "ai", text: msg.ev.text})
			}
			p.busy, p.steps = false, nil
			p.syncContent()
			return p, nil
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			return p, p.send(p.input.Value())
		case "pgup", "pgdown":
			if p.ready {
				var cmd tea.Cmd
				p.vp, cmd = p.vp.Update(msg)
				return p, cmd
			}
			return p, nil
		}
		// number selects a pending proposal (on empty input)
		if s := msg.String(); len(s) == 1 && s >= "1" && s <= "9" && strings.TrimSpace(p.input.Value()) == "" && len(p.props) > 0 {
			if n := int(s[0] - '1'); n < len(p.props) {
				pr := p.props[n]
				return p, func() tea.Msg { return ProposalMsg{Tool: pr.Tool, Args: pr.Args, Job: p.label} }
			}
		}
		if !p.busy {
			var cmd tea.Cmd
			p.input, cmd = p.input.Update(msg)
			return p, cmd
		}
	}
	return p, nil
}

func (p *AIPanel) transcriptText() string {
	var b strings.Builder
	for _, t := range p.turns {
		switch t.who {
		case "you":
			b.WriteString("You: " + t.text + "\n")
		case "ai":
			b.WriteString("Assistant: " + t.text + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func (p *AIPanel) wrap(s string) string {
	if p.wrapW <= 0 {
		return s
	}
	return lipgloss.NewStyle().Width(p.wrapW).Render(s)
}

func (p *AIPanel) content() string {
	var b strings.Builder
	if len(p.turns) == 0 && !p.busy {
		b.WriteString(styles.Dim().Render("Ask about what's on screen. I already have this page's context.") + "\n")
	}
	for _, t := range p.turns {
		switch t.who {
		case "you":
			b.WriteString("\n" + styles.ViewTitle().Render("You") + "\n" + p.wrap(t.text) + "\n")
		case "ai":
			b.WriteString("\n" + styles.OKText().Render("AI") + "\n" + p.wrap(t.text) + "\n")
		case "err":
			b.WriteString("\n" + styles.ErrText().Render(p.wrap("⚠ "+t.text)) + "\n")
		}
	}
	if p.busy {
		for _, s := range p.steps {
			b.WriteString(" " + styles.WarnText().Render(s) + "\n")
		}
		b.WriteString(styles.Dim().Render(" thinking…") + "\n")
	}
	if len(p.props) > 0 {
		b.WriteString("\n" + styles.ErrText().Render("PROPOSED — press the number to review") + "\n")
		for i, pr := range p.props {
			b.WriteString(fmt.Sprintf(" [%d] %s %v\n", i+1, pr.Tool, pr.Args))
		}
	}
	return b.String()
}

func (p *AIPanel) syncContent() {
	if p.ready {
		p.vp.SetContent(p.content())
		p.vp.GotoBottom()
	}
}

// View renders the panel at the given size; focused draws the input cursor.
func (p *AIPanel) View(width, height int, focused bool) string {
	knows := "AI"
	sub := "press A to close"
	if p.label != "" {
		sub = "knows: " + p.label
	}
	body := func(iw, ih int) string {
		p.wrapW = iw
		bodyH := ih - 1
		if bodyH < 1 {
			bodyH = 1
		}
		p.input.Width = iw - 4
		if p.input.Width < 4 {
			p.input.Width = 4
		}
		if !p.ready {
			p.vp = viewport.New(iw, bodyH)
			p.ready = true
		}
		p.vp.Width = iw
		p.vp.Height = bodyH
		p.vp.SetContent(p.content())
		prompt := "> " + p.input.View()
		if p.busy {
			prompt = styles.Dim().Render("(answering — esc to unfocus)")
		} else if !focused {
			prompt = styles.Dim().Render("(tab to focus · A to close)")
		}
		// clip every line to the inner width so a long placeholder/answer can
		// never widen the panel (render-safety at tiny sizes).
		return lipgloss.NewStyle().MaxWidth(iw).Render(p.vp.View() + "\n" + prompt)
	}
	return framed(width, height, knows, -1, sub, body)
}
