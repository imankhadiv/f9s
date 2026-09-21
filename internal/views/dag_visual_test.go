package views

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

func TestSelectedDagNodeKeepsHealthColorAndUsesAccentFocusBorder(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)

	skin := filepath.Join(t.TempDir(), "skin.yaml")
	if err := os.WriteFile(skin, []byte(`
colors:
  accent: "#AA00AA"
  flow: "#123456"
  state: "#654321"
  backpressure: "#111111"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := styles.LoadSkin(skin); err != nil {
		t.Fatal(err)
	}

	d := NewDagView(Deps{}, "job-1", "orders")
	d.job = model.Job{Vertices: []model.Vertex{{ID: "map", Name: "Map", Parallelism: 2}}}
	d.stats["map"] = dagStat{level: "ok", bpMax: 0, busyMax: 10, skew: 0}
	node := model.PlanNode{ID: "map", Inputs: []model.PlanInput{{ID: "source"}}}

	selected := d.box(node, boxInner, true)
	if !strings.Contains(selected, "\x1b[38;2;170;0;170m┏") {
		t.Fatalf("selected border does not use accent color: %q", selected)
	}
	if !strings.Contains(selected, "38;2;18;52;86m") {
		t.Fatalf("selected healthy node lost its flow color: %q", selected)
	}
}

func TestDagHealthUsesDistinctFlinkOperationalStates(t *testing.T) {
	d := NewDagView(Deps{}, "job-1", "orders")
	node := model.PlanNode{ID: "map", Inputs: []model.PlanInput{{ID: "source"}}}

	tests := []struct {
		name string
		stat dagStat
		want boxHealth
	}{
		{name: "steady flow", stat: dagStat{level: "ok", busyMax: 12}, want: hFlow},
		{name: "elevated work", stat: dagStat{level: "low", busyMax: 55}, want: hWarn},
		{name: "busy bottleneck", stat: dagStat{level: "ok", busyMax: 92}, want: hBusy},
		{name: "backpressured", stat: dagStat{level: "high", busyMax: 4}, want: hBackpressured},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.classify(node, tt.stat); got != tt.want {
				t.Fatalf("classify() = %v, want %v", got, tt.want)
			}
		})
	}
}
