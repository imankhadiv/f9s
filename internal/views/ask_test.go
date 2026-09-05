package views

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ai"
	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/tools"
)

// fakeAgent satisfies ai.Agent without any network so send()'s goroutine
// terminates immediately in tests.
type fakeAgent struct{}

func (fakeAgent) Investigate(_ context.Context, _ string, _ *tools.Registry, _ func(string) string, _ ai.StepFn, _ ai.ProposeFn) (string, error) {
	return "ok", nil
}
func (fakeAgent) Ask(_ context.Context, _ string, _ *tools.Registry, _ func(string) string, _ ai.StepFn, _ ai.ProposeFn) (string, error) {
	return "ok", nil
}

func askDeps() Deps {
	return Deps{Ctx: "test", Cfg: &config.Config{Contexts: map[string]config.Context{"test": {URL: "http://x"}}}}
}

func keyMsg(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// A digit on an empty input with a pending proposal opens that proposal.
func TestAskDigitSelectsProposal(t *testing.T) {
	v := NewAskView(askDeps(), "j1", "job", fakeAgent{}, "test")
	v.props = []ai.Proposal{
		{Tool: "trigger_savepoint", Args: map[string]string{"jid": "j1"}},
		{Tool: "cancel_job", Args: map[string]string{"jid": "j1"}},
	}
	_, cmd := v.Update(keyMsg("2"))
	if cmd == nil {
		t.Fatal("digit with a pending proposal should return a command")
	}
	pm, ok := cmd().(ProposalMsg)
	if !ok {
		t.Fatalf("expected a ProposalMsg, got %T", cmd())
	}
	if pm.Tool != "cancel_job" || pm.Args["jid"] != "j1" {
		t.Fatalf("wrong proposal routed: %+v", pm)
	}
}

// A digit on a fresh, empty chat sends the matching starter question.
func TestAskDigitSelectsStarter(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir()) // invlog writes here
	v := NewAskView(askDeps(), "", "", fakeAgent{}, "test")
	// Skip grounding (no live client) and anonymization for a hermetic test.
	v.grounded = true
	v.evidence = "seed"
	v.anon = false
	if len(v.suggestions) == 0 {
		t.Fatal("cluster chat should have starter suggestions")
	}
	first := v.suggestions[0]
	v.Update(keyMsg("1"))
	if len(v.turns) != 1 || v.turns[0].who != "you" || v.turns[0].text != first {
		t.Fatalf("digit did not send the first starter: %+v", v.turns)
	}
	if !v.busy {
		t.Error("sending a question should mark the view busy")
	}
}

// A digit while typing a question is text, not a selection.
func TestAskDigitWhileTypingIsText(t *testing.T) {
	v := NewAskView(askDeps(), "j1", "job", fakeAgent{}, "test")
	v.props = []ai.Proposal{{Tool: "cancel_job", Args: map[string]string{"jid": "j1"}}}
	v.input.SetValue("scale to ")
	_, cmd := v.Update(keyMsg("3"))
	if cmd != nil {
		if _, isProp := cmd().(ProposalMsg); isProp {
			t.Fatal("a digit typed into a non-empty question must not fire a proposal")
		}
	}
	if v.input.Value() != "scale to 3" {
		t.Fatalf("digit should have been typed into the input, got %q", v.input.Value())
	}
	if len(v.turns) != 0 {
		t.Fatalf("no question should have been sent, got %d turns", len(v.turns))
	}
}
