package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/ui/styles"
)

// titleBar renders the one consistent resource title line every list/table
// view shares: an UPPERCASE resource name, an optional dim count, and an
// optional right-aligned meta slot (sort mode, context, tagline). Pass
// count < 0 to omit the count. It is always exactly one line.
func titleBar(width int, title string, count int, right string) string {
	left := styles.ViewTitle().Render(strings.ToUpper(title))
	if count >= 0 {
		left += " " + styles.Dim().Render(fmt.Sprintf("(%d)", count))
	}
	r := ""
	if right != "" {
		r = styles.Dim().Render(right)
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(r) - 1
	if gap < 1 {
		gap = 1
	}
	return " " + left + strings.Repeat(" ", gap) + r
}

// framed wraps a view's content in the titled rounded border (the k9s
// silhouette). The build closure receives the INNER dimensions (border
// subtracted) and must render its table/viewport to them — replacing the old
// "print titleBar, then table at height-used" pattern. The title, count, and
// right-meta are sunk into the top border instead of a separate line.
func framed(width, height int, title string, count int, right string, build func(iw, ih int) string) string {
	iw, ih := width-2, height-2
	if iw < 1 {
		iw = 1
	}
	if ih < 1 {
		ih = 1
	}
	return styles.TitledPanel(title, count, right, build(iw, ih), width, height)
}

// errorBanner renders the consistent one-line error strip used across views.
func errorBanner(msg string) string {
	return styles.ErrText().Render("⚠ "+msg) + "\n"
}

// emptyState renders consistent dim placeholder copy for an empty view.
func emptyState(msg string) string {
	return styles.Dim().Render(msg)
}
