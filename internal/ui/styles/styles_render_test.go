package styles

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

var renderWidths = []int{40, 80, 100, 140}

func TestTitledPanelNeverExceedsWidthOrHeight(t *testing.T) {
	body := strings.Repeat("a very long body line that must be clipped or wrapped ", 6)
	titles := []string{"", "jobs", "a-very-long-resource-title-that-overflows-narrow-terminals"}
	rights := []string{"", "sort: name ▲", strings.Repeat("meta ", 30)}
	for _, w := range append([]int{1, 2, 3, 5, 11, 12, 13, 20}, renderWidths...) {
		for _, title := range titles {
			for _, right := range rights {
				out := TitledPanel(title, 4, right, body, w, 6)
				lines := strings.Split(out, "\n")
				limit := w
				if limit < 3 {
					limit = 3 // TitledPanel clamps tiny widths up to 3
				}
				if len(lines) > 6 {
					t.Errorf("w=%d title=%q: %d lines, want <= 6", w, title, len(lines))
				}
				for i, ln := range lines {
					if got := lipgloss.Width(ln); got > limit {
						t.Errorf("w=%d title=%q right=%q line %d width %d", w, title, right, i, got)
					}
				}
			}
		}
	}
}

func TestTitledPanelShowsUppercasedTitleCountAndMeta(t *testing.T) {
	out := TitledPanel("jobs", 4, "sort: name", "body", 60, 4)
	top := strings.Split(out, "\n")[0]
	for _, want := range []string{"JOBS", "(4)", "sort: name"} {
		if !strings.Contains(top, want) {
			t.Errorf("top edge %q missing %q", top, want)
		}
	}
	if strings.Contains(TitledPanel("jobs", -1, "", "x", 40, 3), "(") {
		t.Error("negative count must hide the count")
	}
}

func TestPanelClampsTinyWidth(t *testing.T) {
	for _, w := range []int{-5, 0, 3} {
		if out := Panel("t", "b", w); out == "" {
			t.Errorf("width %d rendered nothing", w)
		}
	}
	for _, w := range renderWidths {
		for i, ln := range strings.Split(Panel("title", strings.Repeat("x", 300), w), "\n") {
			if got := lipgloss.Width(ln); got > w {
				t.Errorf("w=%d line %d width %d", w, i, got)
			}
		}
	}
}

func TestSparkScalesAndMarksNegatives(t *testing.T) {
	if Spark(nil) != "" {
		t.Error("empty input must render empty")
	}
	got := []rune(Spark([]float64{0, 7, 14}))
	if len(got) != 3 || got[0] != '▁' || got[2] != '█' {
		t.Errorf("Spark = %q, want ▁ … █", string(got))
	}
	if s := Spark([]float64{-1, 0, 0}); !strings.Contains(s, "·") || !strings.Contains(s, "▁") {
		t.Errorf("negative sample should be a dot, got %q", s)
	}
	if s := Spark([]float64{0, 0}); s != "▁▁" {
		t.Errorf("all-zero series = %q", s)
	}
}

func TestGaugeIsFixedWidthAndClamped(t *testing.T) {
	for _, frac := range []float64{-1, 0, 0.33, 0.5, 1, 7} {
		for _, w := range []int{0, 1, 10, 25} {
			want := w
			if want < 1 {
				want = 1
			}
			if got := lipgloss.Width(Gauge(frac, w, 0.7, 0.9)); got != want {
				t.Errorf("frac=%v w=%d: width %d, want %d", frac, w, got, want)
			}
		}
	}
	if g := Gauge(0.5, 10, 0.7, 0.9); strings.Count(g, "█") != 5 || strings.Count(g, "░") != 5 {
		t.Errorf("half gauge = %q", g)
	}
}

func TestSeverityLadder(t *testing.T) {
	cases := []struct {
		v    float64
		want lipgloss.Style
	}{
		{0.1, OKText()}, {0.7, WarnText()}, {0.89, WarnText()}, {0.9, ErrText()}, {5, ErrText()},
	}
	for _, c := range cases {
		if got := Severity(c.v, 0.7, 0.9); got.GetForeground() != c.want.GetForeground() {
			t.Errorf("Severity(%v) foreground = %v, want %v", c.v, got.GetForeground(), c.want.GetForeground())
		}
	}
}

func TestStateAndBackpressureStyles(t *testing.T) {
	ok, warn, bad := OKText().GetForeground(), WarnText().GetForeground(), ErrText().GetForeground()
	dim := Dim().GetForeground()
	for state, want := range map[string]lipgloss.TerminalColor{
		"RUNNING": ok, "FINISHED": ok, "COMPLETED": ok,
		"RESTARTING": warn, "IN_PROGRESS": warn, "CANCELLING": warn,
		"FAILED": bad, "FAILING": bad,
		"CANCELED": dim, "SUSPENDED": dim,
	} {
		if got := StateStyle(state).GetForeground(); got != want {
			t.Errorf("StateStyle(%s) = %v, want %v", state, got, want)
		}
	}
	if _, isNo := StateStyle("WHATEVER").GetForeground().(lipgloss.NoColor); !isNo {
		t.Error("unknown state must render plain")
	}
	for level, want := range map[string]lipgloss.TerminalColor{
		"high": bad, "HIGH": bad, "low": warn, "ok": ok, "": ok, "weird": dim,
	} {
		if got := BPStyle(level).GetForeground(); got != want {
			t.Errorf("BPStyle(%q) = %v, want %v", level, got, want)
		}
	}
}
