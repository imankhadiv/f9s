package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/ui/styles"
)

// suggestRows is how many commands the dropdown shows at once; the rest are
// summarised by an overflow line, so a long list never eats the view.
const suggestRows = 8

// cmdSuggest is the command-mode dropdown: the commands matching what has
// been typed so far, with one highlighted row. Typing filters, ↑↓ moves the
// highlight, tab completes it, enter runs it.
type cmdSuggest struct {
	input   string
	matches []commandTarget
	cursor  int
}

// update refilters for the current input. The highlight is preserved while
// the match set is unchanged (a redraw) and resets to the best match
// whenever the input changes what matched.
func (s *cmdSuggest) update(input string) {
	input = strings.TrimSpace(input)
	next := commandMatches(input)
	if s.input != input || !sameTargets(s.matches, next) {
		s.cursor = 0
	}
	s.input, s.matches = input, next
	s.clamp()
}

func sameTargets(a, b []commandTarget) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].names[0] != b[i].names[0] {
			return false
		}
	}
	return true
}

func (s *cmdSuggest) clamp() {
	if s.cursor >= len(s.matches) {
		s.cursor = len(s.matches) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// move shifts the highlight, stopping at either end.
func (s *cmdSuggest) move(delta int) {
	s.cursor += delta
	s.clamp()
}

// names lists the canonical command names on offer.
func (s *cmdSuggest) names() []string {
	out := make([]string, len(s.matches))
	for i, m := range s.matches {
		out[i] = m.names[0]
	}
	return out
}

// selected is the highlighted command ("" when nothing matches).
func (s *cmdSuggest) selected() string {
	if len(s.matches) == 0 {
		return ""
	}
	return s.matches[s.cursor].names[0]
}

// window returns the slice of rows to draw, keeping the cursor visible.
func (s *cmdSuggest) window() (start, end int) {
	if len(s.matches) <= suggestRows {
		return 0, len(s.matches)
	}
	start = s.cursor - suggestRows + 1
	if start < 0 {
		start = 0
	}
	if start > len(s.matches)-suggestRows {
		start = len(s.matches) - suggestRows
	}
	return start, start + suggestRows
}

// view renders the dropdown, one command per line, fitted to width.
func (s *cmdSuggest) view(width int) string {
	if len(s.matches) == 0 {
		return styles.Dim().Render(fitText("   no command matches :"+s.input, width))
	}
	nameW := 0
	for _, m := range s.matches {
		if n := lipgloss.Width(m.names[0]); n > nameW {
			nameW = n
		}
	}
	start, end := s.window()
	lines := make([]string, 0, suggestRows+1)
	for i := start; i < end; i++ {
		m := s.matches[i]
		marker, name := "  ", styles.Dim()
		if i == s.cursor {
			marker, name = styles.Header().Render("❯ "), styles.OKText()
		}
		// pad the plain name BEFORE styling: escape codes carry no width, so
		// padding a styled string silently loses the alignment
		row := " " + marker + name.Render(fmt.Sprintf("%-*s", nameW, m.names[0])) + "  " + styles.Dim().Render(m.desc)
		plain := fmt.Sprintf(" %s%-*s  %s", "  ", nameW, m.names[0], m.desc)
		if lipgloss.Width(plain) > width {
			row = fitText(plain, width)
			if i == s.cursor {
				row = styles.OKText().Render(row)
			} else {
				row = styles.Dim().Render(row)
			}
		}
		lines = append(lines, row)
	}
	if n := len(s.matches) - (end - start); n > 0 {
		lines = append(lines, styles.Dim().Render(fitText(fmt.Sprintf("   +%d more", n), width)))
	}
	return strings.Join(lines, "\n")
}
