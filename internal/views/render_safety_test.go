package views

import (
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/sentinel"
	"github.com/imankhadiv/f9s/internal/watch"
)

// Every view must construct and render at any terminal size without
// panicking and without emitting a line wider than the viewport — the
// first-run-on-a-narrow-terminal crash class. Views render from their
// zero/empty state here (no live cluster), which is exactly the state a
// stranger sees in the first second.
func TestViewsRenderSafelyAtAnySize(t *testing.T) {
	deps := Deps{
		Ctx:      "test",
		Cfg:      &config.Config{Contexts: map[string]config.Context{"test": {URL: "http://x"}}},
		Sentinel: sentinel.New(),
		Watch:    watch.NewStore(),
	}
	builders := map[string]func() Model{
		"jobs":           func() Model { return NewJobs(deps) },
		"pulse":          func() Model { return NewPulse(deps) },
		"waste":          func() Model { return NewWasteFinder(deps) },
		"timeline":       func() Model { return NewTimeline(deps) },
		"doctor":         func() Model { return NewDoctor(deps) },
		"alerts":         func() Model { return NewAlerts(deps) },
		"savepoints":     func() Model { return NewSavepoints(deps) },
		"taskmanagers":   func() Model { return NewTaskManagers(deps) },
		"jars":           func() Model { return NewJars(deps) },
		"jmlog":          func() Model { return NewJMLog(deps) },
		"restlog":        func() Model { return NewRestLog(deps) },
		"investigations": func() Model { return NewInvestigations(deps) },
		"jmconfig":       func() Model { return NewJMConfig(deps) },
		"contexts":       func() Model { return NewContexts(deps) },
		"jobdetail":      func() Model { return NewJobDetail(deps, "j1", "job") },
		"checkpoints":    func() Model { return NewCheckpoints(deps, "j1", "job") },
		"dag":            func() Model { return NewDagView(deps, "j1", "job") },
		"watermarks":     func() Model { return NewWatermarks(deps, "j1", "job") },
		"subtasks":       func() Model { return NewSubtasks(deps, "j1", "v1", "vtx") },
		"skewdetail":     func() Model { return NewSkewDetail(deps, "j1", "v1", "vtx", 4, true) },
	}
	// App.View refuses to render any view below 60x10 (it shows an
	// enlarge-the-terminal notice), so that is the app's real contract:
	// panic-safety at ANY size, width-safety at the sizes actually rendered.
	panicSizes := [][2]int{{1, 1}, {20, 5}, {40, 10}, {60, 10}, {80, 24}, {200, 60}}
	widthSizes := [][2]int{{60, 10}, {80, 24}, {120, 40}, {200, 60}}
	for name, build := range builders {
		name, build := name, build
		t.Run(name+"/no-panic", func(t *testing.T) {
			for _, sz := range panicSizes {
				w, h := sz[0], sz[1]
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("%s panicked at %dx%d: %v", name, w, h, r)
						}
					}()
					_ = build().View(w, h)
				}()
			}
		})
		t.Run(name+"/no-overflow", func(t *testing.T) {
			for _, sz := range widthSizes {
				w, h := sz[0], sz[1]
				out := build().View(w, h)
				// chrome (titles/subtitles) may exceed width and the terminal
				// clips it; a line more than 2x the viewport indicates real
				// layout breakage (bad wrapping / unbounded content).
				for _, line := range strings.Split(out, "\n") {
					if vw := visibleWidth(line); vw > 2*w {
						t.Errorf("%s at %dx%d: line width %d >> %d (layout breakage):\n%q", name, w, h, vw, w, line)
						break
					}
				}
			}
		})
	}
}

// visibleWidth counts runes outside ANSI escape sequences.
func visibleWidth(s string) int {
	n, inEsc := 0, false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && (r == 'm' || r == 'K' || r == 'H' || r == 'J'):
			inEsc = false
		case inEsc:
		default:
			n++
		}
	}
	return n
}
