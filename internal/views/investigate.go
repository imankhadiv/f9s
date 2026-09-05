package views

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ai"
	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/incident"
	"github.com/imankhadiv/f9s/internal/invlog"
	"github.com/imankhadiv/f9s/internal/runbook"
	"github.com/imankhadiv/f9s/internal/tools"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type agentEvent struct {
	kind     string // "step" / "done" / "err" / "propose"
	text     string
	proposal ai.Proposal
}

type agentEventMsg agentEvent

// Investigate runs the multi-step agentic diagnosis: the model starts from
// the evidence bundle and drills deeper with the read-only tool registry;
// every call it makes renders live (the audit trail IS the UI).
type Investigate struct {
	deps     Deps
	jid      string
	name     string
	agent    ai.Agent
	provider string
	events   chan agentEvent
	steps    []string
	props    []ai.Proposal
	answer   string
	running  bool
	vp       viewport.Model
	ready    bool
	err      string
}

// NewInvestigate builds the view; caller has gated policy and capability.
func NewInvestigate(deps Deps, jid, name string, agent ai.Agent, providerName string) *Investigate {
	return &Investigate{
		deps: deps, jid: jid, name: name, agent: agent, provider: providerName,
		events: make(chan agentEvent, 16), running: true,
	}
}

func (v *Investigate) Title() string   { return "investigate" }
func (v *Investigate) Filtering() bool { return false }
func (v *Investigate) Bindings() []key.Binding {
	return []key.Binding{
		binding("up", "↑↓/pgup", "scroll"),
		binding("esc", "esc", "back"),
	}
}

func (v *Investigate) listen() tea.Cmd {
	ch := v.events
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return agentEventMsg(agentEvent{kind: "done", text: ""})
		}
		return agentEventMsg(ev)
	}
}

func (v *Investigate) Init() tea.Cmd {
	cl := v.deps.CL
	jid, name := v.jid, v.name
	agent := v.agent
	reg := tools.New(cl, v.deps.Ctx)
	anonymize := v.deps.Cfg.Contexts[v.deps.Ctx].AI.AnonymizeOn()
	ch := v.events
	ctxName := v.deps.Ctx
	go func() {
		defer close(ch)
		lg := invlog.New(ctxName, jid, name, "manual")
		seed, err := analyze.BuildEvidence(cl, jid)
		if err != nil {
			lg.Close("", "evidence: "+err.Error())
			ch <- agentEvent{kind: "err", text: "evidence: " + err.Error()}
			return
		}
		seed = runbook.Inject(seed, name, "")
		seed = incident.Inject(ctxName, seed, name, "")
		fp := incident.Extract(seed, name, "")
		scrubber := analyze.NewScrubber([]string{name})
		scrub := func(s string) string { return s }
		if anonymize {
			seed = scrubber.Scrub(seed)
			scrub = scrubber.Scrub
		}
		answer, err := agent.Investigate(context.Background(), seed, reg, scrub,
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
		if anonymize {
			answer = scrubber.Restore(answer)
		}
		lg.Close(answer, "")
		incident.Append(ctxName, incident.Record{Time: time.Now(), JID: jid, FP: fp, Hypothesis: incident.HypothesisOf(answer)})
		ch <- agentEvent{kind: "done", text: answer}
	}()
	return v.listen()
}

// Refresh is a no-op: an investigation is a one-shot pipeline.
func (v *Investigate) Refresh() tea.Cmd { return nil }

func (v *Investigate) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case agentEventMsg:
		switch msg.kind {
		case "step":
			v.steps = append(v.steps, msg.text)
			v.syncContent()
			return v, v.listen()
		case "propose":
			v.props = append(v.props, msg.proposal)
			v.syncContent()
			return v, v.listen()
		case "err":
			v.err = msg.text
			v.running = false
			v.syncContent()
			return v, v.listen()
		case "done":
			if msg.text != "" {
				v.answer = msg.text
			}
			v.running = false
			v.syncContent()
			return v, nil
		}
	case tea.KeyMsg:
		if k := msg.String(); len(k) == 1 && k >= "1" && k <= "9" {
			idx := int(k[0] - '1')
			if idx < len(v.props) {
				p := v.props[idx]
				job := v.name
				return v, func() tea.Msg { return ProposalMsg{Tool: p.Tool, Args: p.Args, Job: job} }
			}
		}
		if v.ready {
			var cmd tea.Cmd
			v.vp, cmd = v.vp.Update(msg)
			return v, cmd
		}
	}
	return v, nil
}

func (v *Investigate) content() string {
	var b strings.Builder
	b.WriteString(styles.Dim().Render("investigation steps (the audit trail):") + "\n")
	if len(v.steps) == 0 {
		b.WriteString(styles.Dim().Render("  (none — the evidence bundle sufficed)") + "\n")
	}
	for _, s := range v.steps {
		b.WriteString(" " + styles.WarnText().Render(s) + "\n")
	}
	if v.running {
		b.WriteString(styles.Dim().Render("\n thinking…") + "\n")
	}
	if v.err != "" {
		b.WriteString("\n" + styles.ErrText().Render("⚠ "+v.err) + "\n")
	}
	if v.answer != "" {
		b.WriteString("\n" + styles.OKText().Render("DIAGNOSIS ("+v.provider+")") + "\n\n" + v.answer + "\n")
	}
	if len(v.props) > 0 {
		b.WriteString("\n" + styles.ErrText().Render("PROPOSED ACTIONS (nothing runs without your confirmation)") + "\n")
		for i, p := range v.props {
			b.WriteString(fmt.Sprintf(" [%d] %s %v\n", i+1, p.Tool, p.Args))
		}
		b.WriteString(styles.Dim().Render(" press the number to review in a confirm dialog") + "\n")
	}
	return b.String()
}

func (v *Investigate) syncContent() {
	if v.ready {
		v.vp.SetContent(v.content())
		v.vp.GotoBottom()
	}
}

func (v *Investigate) View(width, height int) string {
	head := styles.ViewTitle().Render("Agentic investigate · "+v.name) + "  " +
		styles.Dim().Render(v.provider+" · read-only tools · max 10 steps") + "\n"
	if !v.ready {
		v.vp = viewport.New(width, height-1)
		v.ready = true
	}
	v.vp.Width = width
	v.vp.Height = height - 1
	v.vp.SetContent(v.content())
	return head + v.vp.View()
}
