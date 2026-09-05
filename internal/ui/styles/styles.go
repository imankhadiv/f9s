// Package styles centralizes the f9s look: an adaptive palette that stays
// legible on light and dark terminals, with Flink-squirrel coral as accent.
package styles

import (
	"github.com/charmbracelet/lipgloss"
)

// The palette is a set of semantic tokens (DESIGN.md #14). Defaults adapt
// to light/dark terminals; skin.yaml overrides them via LoadSkin.
var (
	coral lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#C74B63", Dark: "#E0607A"}
	dim   lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#665E72", Dark: "#8B8497"}
	green lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#2E8B57", Dark: "#5FBF8B"}
	amber lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#B07C1F", Dark: "#D4A23F"}
	red   lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#B3372E", Dark: "#E06055"}
	selFg lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#1B1118", Dark: "#F5E1E4"}
	selBg lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#F0D4DA", Dark: "#4A2833"}
)

// tokenTargets maps skin.yaml token names onto the palette.
var tokenTargets = map[string]*lipgloss.TerminalColor{
	"accent":      &coral,
	"dim":         &dim,
	"ok":          &green,
	"warn":        &amber,
	"error":       &red,
	"selectionFg": &selFg,
	"selectionBg": &selBg,
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
