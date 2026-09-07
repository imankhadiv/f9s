package views

import (
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

func TestAIPanelRendersSafely(t *testing.T) {
	deps := Deps{Ctx: "test", Cfg: &config.Config{Contexts: map[string]config.Context{"test": {URL: "http://x"}}}}
	p := NewAIPanel(deps)
	for _, sz := range [][2]int{{1, 1}, {20, 6}, {34, 20}, {56, 40}, {80, 24}} {
		out := p.View(sz[0], sz[1], true)
		for _, ln := range strings.Split(out, "\n") {
			if visibleWidth(ln) > 2*sz[0]+2 {
				t.Errorf("panel line wider than viewport at %v: %q", sz, ln)
			}
		}
	}
}

func TestAIContextFallback(t *testing.T) {
	// a view that doesn't implement AIContexter falls back to Title()
	deps := Deps{Ctx: "test", Cfg: &config.Config{Contexts: map[string]config.Context{"test": {URL: "http://x"}}}}
	label, ctx, _ := AIContextOf(NewJobs(deps))
	if label == "" || ctx == "" {
		t.Fatalf("fallback context empty: %q %q", label, ctx)
	}
}
