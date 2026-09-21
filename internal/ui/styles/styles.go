// Package styles centralizes the f9s look: an adaptive palette that stays
// legible on light and dark terminals, with Flink-squirrel coral as accent.
package styles

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Sort-indicator glyphs for the active table column.
const (
	SortDesc = "▾"
	SortAsc  = "▴"
)

// The palette is a set of semantic tokens (DESIGN.md #14). Defaults adapt
// to light/dark terminals; skin.yaml overrides them via LoadSkin.
var (
	coral        lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#8A3143", Dark: "#E6526F"}
	dim          lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#545454", Dark: "#B9BFC7"}
	green        lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#2E8B57", Dark: "#5FBF8B"}
	amber        lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#93601D", Dark: "#F5A030"}
	red          lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#B3372E", Dark: "#E06055"}
	selFg        lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#171717", Dark: "#E5EAF4"}
	selBg        lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#E5EAF4", Dark: "#323232"}
	flow         lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#5B9BF5"}
	state        lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#734591", Dark: "#BF73F2"}
	backpressure lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#171717", Dark: "#E5EAF4"}
)

// Raw palette-color accessors, for callers that need a TerminalColor (e.g. a
// box border) rather than a Style.
func GreenColor() lipgloss.TerminalColor        { return green }
func AmberColor() lipgloss.TerminalColor        { return amber }
func RedColor() lipgloss.TerminalColor          { return red }
func DimColor() lipgloss.TerminalColor          { return dim }
func AccentColor() lipgloss.TerminalColor       { return coral }
func FlowColor() lipgloss.TerminalColor         { return flow }
func StateColor() lipgloss.TerminalColor        { return state }
func BackpressureColor() lipgloss.TerminalColor { return backpressure }

// FlowText styles idle/steady operators using Flink's operational blue.
func FlowText() lipgloss.Style { return lipgloss.NewStyle().Foreground(flow) }

// StateText styles stateful activity such as checkpoints and pending rescale.
func StateText() lipgloss.Style { return lipgloss.NewStyle().Foreground(state) }

// BackpressureText styles operators blocked by downstream work. Flink uses
// black for this state; the dark-terminal variant becomes a high-contrast
// neutral so it remains visible.
func BackpressureText() lipgloss.Style { return lipgloss.NewStyle().Foreground(backpressure) }

// tokenTargets maps skin.yaml token names onto the palette.
var tokenTargets = map[string]*lipgloss.TerminalColor{
	"accent":       &coral,
	"dim":          &dim,
	"ok":           &green,
	"warn":         &amber,
	"error":        &red,
	"selectionFg":  &selFg,
	"selectionBg":  &selBg,
	"flow":         &flow,
	"state":        &state,
	"backpressure": &backpressure,
}

// Header styles the top cluster bar.
func Header() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(coral).Bold(true)
}

// Dim styles secondary text.
func Dim() lipgloss.Style { return lipgloss.NewStyle().Foreground(dim) }

// ViewTitle styles a view's heading.
func ViewTitle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(amber).Bold(true)
}

// ErrText styles error/flash messages.
func ErrText() lipgloss.Style { return lipgloss.NewStyle().Foreground(red) }

// WarnText styles degraded-but-not-dead states.
func WarnText() lipgloss.Style { return lipgloss.NewStyle().Foreground(amber) }

// OKText styles healthy-state text.
func OKText() lipgloss.Style { return lipgloss.NewStyle().Foreground(green) }

// Footer styles the keybinding hint bar.
func Footer() lipgloss.Style { return lipgloss.NewStyle().Foreground(dim) }

// TableHeader styles column titles.
func TableHeader() lipgloss.Style { return lipgloss.NewStyle().Foreground(dim).Bold(true) }

// TableBorder styles the header separator line.
func TableBorder() lipgloss.Style { return lipgloss.NewStyle().Foreground(dim) }

// SelectedRow styles the table selection band.
func SelectedRow() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(selFg).Background(selBg).Bold(true)
}

// ModalBox styles the centered confirm/input dialog.
func ModalBox() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(red).
		Padding(1, 3)
}

// Accent styles text in the brand accent without bold.
func Accent() lipgloss.Style { return lipgloss.NewStyle().Foreground(coral) }

// PanelBox is a rounded, dim-bordered container for header chrome and
// dashboard tiles. Width sets the content area (add 2 for the border).
func PanelBox() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(dim).
		Padding(0, 1)
}

// Panel renders a titled, rounded tile of the given TOTAL width. The title
// sits in accent bold above the body; both wrap/clip to the inner width.
func Panel(title, body string, width int) string {
	if width < 4 {
		width = 4
	}
	content := body
	if title != "" {
		content = Accent().Bold(true).Render(title) + "\n" + body
	}
	return PanelBox().Width(width - 2).Render(content)
}

// TitledPanel wraps body in a rounded border of exactly width×height with a
// styled resource title (and optional count + right-meta) sunk into the top
// edge — the k9s silhouette:
//
//	╭─ JOBS (4) ─────────────────── sort: name ─╮
//	│ …table…                                    │
//	╰────────────────────────────────────────────╯
//
// It is safe at any size: below a usable width it degrades to a plain rounded
// box, and the title/meta drop or truncate before any line can exceed width.
func TitledPanel(title string, count int, right, body string, width, height int) string {
	if width < 2 {
		width = 2
	}
	if height < 2 {
		height = 2
	}
	b := lipgloss.RoundedBorder()
	// Below a usable width, skip the title entirely — a plain rounded box.
	if width < 12 || title == "" {
		return lipgloss.NewStyle().Border(b).BorderForeground(dim).
			Width(width - 2).Height(height - 2).MaxHeight(height - 2).Render(body)
	}
	inner := width - 2

	lbl := ViewTitle().Render(strings.ToUpper(title))
	if count >= 0 {
		lbl += " " + Dim().Render(fmt.Sprintf("(%d)", count))
	}
	label := " " + lbl + " "
	rightSeg := ""
	if right != "" {
		rightSeg = " " + Dim().Render(right) + " "
	}
	lw, rw := lipgloss.Width(label), lipgloss.Width(rightSeg)
	if lw+rw > inner { // drop the meta before overflowing
		rightSeg, rw = "", 0
	}
	if lw > inner { // truncate the raw title, then re-style, to stay width-exact
		raw := runewidth.Truncate(strings.ToUpper(title), inner-2, "…")
		label = " " + ViewTitle().Render(raw) + " "
		lw = lipgloss.Width(label)
	}
	dash := inner - lw - rw
	if dash < 0 {
		dash = 0
	}
	left := 1
	if dash < left {
		left = dash
	}
	mid := dash - left
	dm := Dim()
	top := dm.Render(b.TopLeft) + dm.Render(strings.Repeat(b.Top, left)) + label +
		dm.Render(strings.Repeat(b.Top, mid)) + rightSeg + dm.Render(b.TopRight)

	boxed := lipgloss.NewStyle().
		Border(b, false, true, true, true). // top edge is the manual one above
		BorderForeground(dim).
		Width(inner).
		Height(height - 2).
		MaxHeight(height - 2).
		Render(body)
	return top + "\n" + boxed
}

// Severity picks OK/Warn/Err by how far a 0..1 (or 0..100) value has climbed
// past the warn and crit thresholds — the one place the "how hot is it"
// ladder lives, so views stop re-implementing it.
func Severity(v, warnAt, critAt float64) lipgloss.Style {
	switch {
	case v >= critAt:
		return ErrText()
	case v >= warnAt:
		return WarnText()
	default:
		return OKText()
	}
}

// BPStyle colors a backpressure level word (ok / low / high).
func BPStyle(level string) lipgloss.Style {
	switch strings.ToLower(level) {
	case "high":
		return ErrText()
	case "low":
		return WarnText()
	case "ok", "":
		return OKText()
	default:
		return Dim()
	}
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// Spark renders a unicode sparkline; negative samples show as a dim dot.
func Spark(vals []float64) string {
	if len(vals) == 0 {
		return ""
	}
	max := 0.0
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	var b strings.Builder
	for _, v := range vals {
		if v < 0 {
			b.WriteString(Dim().Render("·"))
			continue
		}
		idx := 0
		if max > 0 {
			idx = int(v / max * float64(len(sparkRunes)-1))
			if idx < 0 {
				idx = 0
			}
			if idx >= len(sparkRunes) {
				idx = len(sparkRunes) - 1
			}
		}
		b.WriteRune(sparkRunes[idx])
	}
	return b.String()
}

// Gauge renders a fixed-width fill bar, severity-colored by fraction.
func Gauge(frac float64, w int, warnAt, critAt float64) string {
	if w < 1 {
		w = 1
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	fill := int(frac*float64(w) + 0.5)
	if fill > w {
		fill = w
	}
	return Severity(frac, warnAt, critAt).Render(strings.Repeat("█", fill)) +
		Dim().Render(strings.Repeat("░", w-fill))
}

// StateStyle returns the color for a Flink state word (job, checkpoint, or
// backpressure vocabulary); unknown states render plain.
func StateStyle(state string) lipgloss.Style {
	switch state {
	case "RUNNING", "FINISHED", "COMPLETED":
		return OKText()
	case "RESTARTING", "IN_PROGRESS", "CANCELLING", "RECONCILING":
		return WarnText()
	case "FAILED", "FAILING":
		return ErrText()
	case "CANCELED", "SUSPENDED":
		return Dim()
	default:
		return lipgloss.NewStyle()
	}
}
