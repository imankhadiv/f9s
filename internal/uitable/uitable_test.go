package uitable

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func newTest() *Model {
	m := New([]Column{{Title: "NAME", Width: -1}, {Title: "STATE", Width: 8}})
	m.SetSize(40, 6)
	m.SetRows([][]Cell{
		{Plain("alpha"), Plain("RUNNING")},
		{Plain("bravo"), Plain("FAILED")},
		{Plain("charlie"), Plain("RUNNING")},
	})
	return m
}

func TestCursorNavigationClamped(t *testing.T) {
	m := newTest()
	m.Update(key("j"))
	m.Update(key("j"))
	m.Update(key("j")) // past the end
	if m.Cursor() != 2 {
		t.Fatalf("cursor should clamp at 2, got %d", m.Cursor())
	}
	m.Update(key("g"))
	if m.Cursor() != 0 {
		t.Fatalf("g should jump home, got %d", m.Cursor())
	}
	m.Update(key("G"))
	if m.Cursor() != 2 {
		t.Fatalf("G should jump to end, got %d", m.Cursor())
	}
}

func TestSetRowsClampsCursor(t *testing.T) {
	m := newTest()
	m.Update(key("G"))
	m.SetRows([][]Cell{{Plain("only"), Plain("RUNNING")}})
	if m.Cursor() != 0 {
		t.Fatalf("cursor should clamp after shrink, got %d", m.Cursor())
	}
}

func TestFitTruncatesWithEllipsis(t *testing.T) {
	got := fit("a-very-long-name", 8)
	if len([]rune(got)) == 0 || !strings.Contains(got, "…") {
		t.Fatalf("expected ellipsis truncation, got %q", got)
	}
	if fit("ab", 5) != "ab   " {
		t.Fatalf("expected padding, got %q", fit("ab", 5))
	}
}

func TestViewLayoutAndSelectionBand(t *testing.T) {
	m := newTest()
	m.SelectedStyle = lipgloss.NewStyle().Reverse(true)
	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) != 6 {
		t.Fatalf("view must pad to height 6, got %d lines", len(lines))
	}
	if !strings.Contains(lines[0], "NAME") || !strings.Contains(lines[0], "STATE") {
		t.Fatalf("missing header: %q", lines[0])
	}
	// selected row (cursor=0) must be one continuous styled span: at most one
	// ANSI reset (zero when the test env strips color) — a broken band shows
	// several resets mid-line.
	sel := lines[2]
	if n := strings.Count(sel, "\x1b[0m"); n > 1 {
		t.Fatalf("selection band broken: %d resets in %q", n, sel)
	}
}

func TestScrollFollowsCursor(t *testing.T) {
	m := New([]Column{{Title: "N", Width: -1}})
	m.SetSize(20, 4) // 2 visible rows
	rows := make([][]Cell, 10)
	for i := range rows {
		rows[i] = []Cell{Plain(strings.Repeat("x", i+1))}
	}
	m.SetRows(rows)
	m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	out := m.View()
	if !strings.Contains(out, strings.Repeat("x", 10)) {
		t.Fatal("view should scroll to show the last row")
	}
}

func TestSetRowsKeyedKeepsSelection(t *testing.T) {
	m := New([]Column{{Title: "A", Width: 10}})
	m.SetRowsKeyed([][]Cell{{Plain("j1")}, {Plain("j2")}, {Plain("j3")}}, []string{"j1", "j2", "j3"})
	m.Update(key("j")) // → j2
	// re-sort: j2 moves to the top
	m.SetRowsKeyed([][]Cell{{Plain("j2")}, {Plain("j3")}, {Plain("j1")}}, []string{"j2", "j3", "j1"})
	if m.Cursor() != 0 {
		t.Fatalf("cursor should follow j2 to index 0, got %d", m.Cursor())
	}
	// key disappears: cursor clamps without following anything
	m.SetRowsKeyed([][]Cell{{Plain("j3")}}, []string{"j3"})
	if m.Cursor() != 0 {
		t.Fatalf("cursor should clamp to 0, got %d", m.Cursor())
	}
}

func TestFilterMapsCursorToOriginalIndex(t *testing.T) {
	m := New([]Column{{Title: "A", Width: 10}})
	m.SetRows([][]Cell{{Plain("alpha")}, {Plain("bravo")}, {Plain("beta")}})
	m.SetFilter("b")
	if m.Len() != 2 {
		t.Fatalf("filter 'b' should show 2 rows, got %d", m.Len())
	}
	if m.Cursor() != 1 {
		t.Fatalf("first visible row is original index 1 (bravo), got %d", m.Cursor())
	}
	m.Update(key("j"))
	if m.Cursor() != 2 {
		t.Fatalf("second visible row is original index 2 (beta), got %d", m.Cursor())
	}
	m.SetFilter("")
	if m.Cursor() != 2 {
		t.Fatalf("clearing the filter keeps the selection, got %d", m.Cursor())
	}
	m.SetFilter("zzz")
	if m.Len() != 0 {
		t.Fatalf("no matches expected, got %d", m.Len())
	}
}

// Wide tables must never emit a line wider than the viewport: the frame
// wraps overlong lines, splitting every row in two (seen on the job-detail
// vertex table in ~105-column terminals).
func TestViewNeverExceedsWidth(t *testing.T) {
	cols := []Column{{Title: "VERTEX", Width: -1}, {Title: "PAR", Width: 4}, {Title: "STATUS", Width: 11},
		{Title: "BACKPRESSURE", Width: 13}, {Title: "BUSY", Width: 5}, {Title: "WM LAG", Width: 8},
		{Title: "SKEW", Width: 10}, {Title: "IN/s", Width: 9, Right: true}, {Title: "OUT/s", Width: 9, Right: true}}
	row := []Cell{Plain("Source: Car data generator"), Plain("1"), Plain("RUNNING"),
		Styled("● ok 0%", lipgloss.NewStyle().Bold(true)), Plain("0%"), Plain("-"), Plain("-"), Plain("10"), Plain("10")}
	for _, w := range []int{40, 90, 100, 105, 110, 140} {
		m := New(cols)
		m.SetRows([][]Cell{row, row})
		m.SetSize(w, 6)
		for i, l := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(l); got > w {
				t.Fatalf("width %d: line %d is %d cells wide", w, i, got)
			}
		}
		if w >= 100 && !strings.Contains(m.View(), "OUT/s") {
			t.Fatalf("width %d: OUT/s column was clipped although the flex column could shrink", w)
		}
	}
}
