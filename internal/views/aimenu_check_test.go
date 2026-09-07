package views

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/model"
)

func TestJobsAOpensAIMenu(t *testing.T) {
	deps := Deps{Ctx: "test", Cfg: &config.Config{Contexts: map[string]config.Context{"test": {URL: "http://x"}}}}
	j := NewJobs(deps)
	j.all = []model.JobOverview{{JID: "j1", Name: "job", State: "RUNNING"}}
	j.applyFilter()
	_, cmd := j.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd == nil {
		t.Fatal("pressing 'a' returned no command")
	}
	pm, ok := cmd().(PushMsg)
	if !ok {
		t.Fatalf("expected PushMsg, got %T", cmd())
	}
	if _, ok := pm.M.(*AIMenu); !ok {
		t.Fatalf("expected 'a' to push *AIMenu, got %T", pm.M)
	}
}

func TestAIMenuSelectRoutes(t *testing.T) {
	deps := Deps{Ctx: "test", Cfg: &config.Config{Contexts: map[string]config.Context{"test": {URL: "http://x", AI: config.AI{Policy: "allow", Provider: "ollama", Model: "m"}}}}}
	m := NewAIMenu(deps, "j1", "job")
	// pressing enter on the first option should return a batch (pop + action)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on AI menu returned no command")
	}
}

func TestAIMenuRenders(t *testing.T) {
	deps := Deps{Ctx: "test", Cfg: &config.Config{Contexts: map[string]config.Context{"test": {URL: "http://x"}}}}
	out := NewAIMenu(deps, "j1", "job").View(90, 22)
	for _, want := range []string{"Explain", "Investigate", "Ask", "AI"} {
		if !contains(out, want) {
			t.Errorf("AI menu missing %q; got:\n%s", want, out)
		}
	}
}

func contains(s, sub string) bool { return len(s) > 0 && (len(sub) == 0 || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
