package views

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/fleet"
	"github.com/imankhadiv/f9s/internal/metricts"
)

func TestFleetViewRendersRingAndFlags(t *testing.T) {
	ring := metricts.New(0)
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		at := now.Add(time.Duration(i-4) * 15 * time.Second)
		add := func(scope, id, m string, v float64) {
			ring.Add(metricts.Key{Scope: scope, ID: id, Metric: m}, metricts.Point{Time: at, Value: v})
		}
		add(metricts.ScopeTM, "tm-1", fleet.HeapUsed, 960)
		add(metricts.ScopeTM, "tm-1", fleet.HeapMax, 1000)
		add(metricts.ScopeTM, "tm-1", fleet.GCTime, float64(i)*6000) // 400 ms/s
		add(metricts.ScopeJM, fleet.JMID, fleet.HeapUsed, 100)
		add(metricts.ScopeJM, fleet.JMID, fleet.HeapMax, 1000)
	}
	v := NewFleet(Deps{Metrics: ring})
	v.now = func() time.Time { return now }
	if v.Init() != nil {
		t.Fatal("a primed ring needs no immediate collection")
	}
	body := v.View(200, 20)
	for _, want := range []string{"jobmanager", "tm-1", "96%", "40%", "GC thrashing", "1 flagged"} {
		if !strings.Contains(body, want) {
			t.Errorf("fleet view missing %q:\n%s", want, body)
		}
	}
	if ctx, _ := v.AIContext(); !strings.Contains(ctx, "[crit] GC thrashing") {
		t.Errorf("AI context:\n%s", ctx)
	}
	if _, cmd := v.Update(keyMsg("enter")); cmd != nil {
		t.Fatal("enter on the JobManager row has nowhere to go")
	}
	v.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := v.Update(keyMsg("enter"))
	if pm, ok := cmd().(PushMsg); !ok || pm.M.Title() != "tm-1" {
		t.Fatalf("enter on a TM row must open its detail, got %#v", cmd())
	}
}

func TestFleetViewCollectsOnceWhenEmpty(t *testing.T) {
	v := NewFleet(Deps{Metrics: metricts.New(0)})
	calls := 0
	v.collect = func() { calls++ }
	cmd := v.Init()
	if cmd == nil {
		t.Fatal("empty ring must trigger one collection")
	}
	cmd()
	if calls != 1 || v.Init() != nil {
		t.Fatalf("collection must run exactly once (calls=%d)", calls)
	}
}
