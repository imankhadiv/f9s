package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/sentinel"
	"github.com/imankhadiv/f9s/internal/views"
)

func TestSuggestFiltersByPrefix(t *testing.T) {
	var s cmdSuggest
	s.update("a")
	if got := s.names(); len(got) != 2 || got[0] != "alerts" || got[1] != "ask" {
		t.Fatalf(`":a" suggests %v, want [alerts ask]`, got)
	}
	if s.selected() != "alerts" {
		t.Fatalf("first match must start highlighted, got %q", s.selected())
	}
}

func TestSuggestListsEverythingForABareColon(t *testing.T) {
	var s cmdSuggest
	s.update("")
	if len(s.names()) != len(commandTargets) {
		t.Fatalf(`":" suggests %d commands, want all %d`, len(s.names()), len(commandTargets))
	}
}

func TestSuggestExactAliasWins(t *testing.T) {
	var s cmdSuggest
	s.update("log") // "ledger" also starts with "l", but "log" is exact
	if got := s.names(); len(got) != 1 || got[0] != "log" {
		t.Fatalf(`":log" suggests %v, want [log]`, got)
	}
}

func TestSuggestCursorMovesAndClamps(t *testing.T) {
	var s cmdSuggest
	s.update("c") // cluster, contexts, log, config, ask, waste
	n := len(s.names())
	if n < 3 {
		t.Fatalf("expected several matches, got %d", n)
	}
	s.move(-1)
	if s.cursor != 0 {
		t.Fatalf("cursor went above the first row: %d", s.cursor)
	}
	s.move(1)
	if s.selected() != s.names()[1] {
		t.Fatalf("down selected %q, want %q", s.selected(), s.names()[1])
	}
	for i := 0; i < n+5; i++ {
		s.move(1)
	}
	if s.cursor != n-1 {
		t.Fatalf("cursor ran past the last row: %d of %d", s.cursor, n)
	}
	// retyping resets the highlight to the best match
	s.update("co")
	if s.cursor != 0 {
		t.Fatalf("editing the input left the cursor at %d", s.cursor)
	}
}

func TestSuggestKeepsSelectionWhenMatchesAreUnchanged(t *testing.T) {
	var s cmdSuggest
	s.update("c")
	s.move(1)
	want := s.selected()
	s.update("c") // same input, e.g. a redraw
	if s.selected() != want {
		t.Fatalf("redraw reset the highlight from %q to %q", want, s.selected())
	}
}

func TestSuggestNoMatches(t *testing.T) {
	var s cmdSuggest
	s.update("zzz")
	if len(s.names()) != 0 || s.selected() != "" {
		t.Fatalf("unknown input suggests %v / %q", s.names(), s.selected())
	}
	if !strings.Contains(s.view(80), "no command matches") {
		t.Fatalf("view = %q", s.view(80))
	}
}

func TestSuggestViewShowsDescriptionsAndHighlight(t *testing.T) {
	var s cmdSuggest
	s.update("a")
	out := s.view(100)
	for _, want := range []string{"alerts", "ask", "ask the AI (cluster-wide)", "❯"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "❯") != 1 {
		t.Errorf("exactly one row is highlighted:\n%s", out)
	}
}

func TestSuggestViewIsCappedAndWidthSafe(t *testing.T) {
	var s cmdSuggest
	s.update("") // every command
	out := s.view(60)
	lines := strings.Split(out, "\n")
	if len(lines) != suggestRows+1 {
		t.Fatalf("view has %d lines, want %d rows + the overflow line", len(lines), suggestRows)
	}
	if !strings.Contains(lines[len(lines)-1], "more") {
		t.Fatalf("overflow line = %q", lines[len(lines)-1])
	}
	for _, width := range []int{60, 80, 200} {
		for _, l := range strings.Split(s.view(width), "\n") {
			if got := lipgloss.Width(l); got > width {
				t.Errorf("width %d: row rendered %d columns: %q", width, got, l)
			}
		}
	}
}

// The highlight follows the cursor out of the first window of rows.
func TestSuggestViewScrollsToTheCursor(t *testing.T) {
	var s cmdSuggest
	s.update("")
	for i := 0; i < len(s.names())-1; i++ {
		s.move(1)
	}
	out := s.view(100)
	last := s.names()[len(s.names())-1]
	if !strings.Contains(out, last) {
		t.Fatalf("view does not follow the cursor to %q:\n%s", last, out)
	}
}

// The dropdown renders above the prompt, and nothing on screen overflows.
func TestCommandModeScreenShowsDropdown(t *testing.T) {
	a := &App{
		width: 100, height: 30, cmdMode: true,
		cmdInput:    newCmdInput(),
		stack:       []views.Model{&helpTestView{}},
		cfg:         &config.Config{Contexts: map[string]config.Context{"test": {URL: "http://unused"}}},
		contextName: "test",
		everSeen:    true,
		sent:        sentinel.NewPersistent("test"),
	}
	a.cmdInput.SetValue("a")
	a.cmdSug.update("a")

	lines := strings.Split(a.View(), "\n")
	var promptAt, alertsAt int
	for i, l := range lines {
		if lipgloss.Width(l) > 100 {
			t.Errorf("line %d is %d columns:\n%s", i, lipgloss.Width(l), l)
		}
		if strings.Contains(l, "alerts") && alertsAt == 0 {
			alertsAt = i
		}
		if strings.Contains(l, "tab complete") {
			promptAt = i
		}
	}
	if alertsAt == 0 || promptAt == 0 || alertsAt > promptAt {
		t.Fatalf("dropdown (line %d) must render above the prompt (line %d)", alertsAt, promptAt)
	}
}

// Descriptions line up in a column: the name is padded before it is styled.
func TestSuggestViewAlignsDescriptions(t *testing.T) {
	var s cmdSuggest
	s.update("")
	lines := strings.Split(s.view(120), "\n")
	col := -1
	for i, line := range lines {
		if i >= suggestRows {
			break // the "+N more" line
		}
		plain := ansi.Strip(line)
		byteAt := strings.LastIndex(plain, s.matches[i].desc)
		if byteAt < 0 {
			t.Fatalf("row %d lost its description: %q", i, plain)
		}
		at := lipgloss.Width(plain[:byteAt]) // columns, not bytes ("❯" is 3 bytes)
		if col == -1 {
			col = at
		} else if at != col {
			t.Fatalf("description starts at column %d, want %d: %q", at, col, plain)
		}
	}
}
