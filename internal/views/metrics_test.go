package views

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
)

func TestInspectorSubtaskBars(t *testing.T) {
	m := NewMetrics(Deps{Ctx: "test"}, model.JobOverview{JID: "j1", Name: "job"})
	it := metricItem{display: "v / numRecordsOut", path: "/x", keys: []string{"numRecordsOut"}, vid: "v1", jid: "j1", par: 2}
	m.inspect = &it
	mm, _ := m.Update(metricValueMsg{display: it.display, value: 87, agg: "min 0 · avg 87 · max 174", skew: 100, vals: []float64{174, 0}})
	m = mm.(*MetricsExplorer)
	out := m.View(120, 40)
	if !strings.Contains(out, "<s> shows the per-subtask bars") {
		t.Fatalf("hint missing:\n%s", out)
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = mm.(*MetricsExplorer)
	out = m.View(120, 40)
	if !strings.Contains(out, "#0") || !strings.Contains(out, "174.0") {
		t.Fatalf("bars missing after s:\n%s", out)
	}
}

func TestSearchPrefixJumpsScope(t *testing.T) {
	m := NewMetrics(Deps{Ctx: "test"}, model.JobOverview{})
	m.catalogs[scopeTM] = []metricItem{{display: "Status.JVM.Memory.Heap.Used", path: "/tm", keys: []string{"x"}}}
	m.input.SetValue("tm:heap")
	m.search()
	if m.scope != scopeTM {
		t.Fatalf("scope = %d, want TM", m.scope)
	}
	if len(m.matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(m.matches))
	}
}
