package ui

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/views"
)

type helpTestView struct {
	updates int
}

func (*helpTestView) Init() tea.Cmd           { return nil }
func (*helpTestView) View(int, int) string    { return "" }
func (*helpTestView) Title() string           { return "test" }
func (*helpTestView) Refresh() tea.Cmd        { return nil }
func (*helpTestView) Filtering() bool         { return false }
func (*helpTestView) Bindings() []key.Binding { return nil }
func (v *helpTestView) Update(tea.Msg) (views.Model, tea.Cmd) {
	v.updates++
	return v, nil
}

func TestHelpOverlayQClosesWithoutQuitting(t *testing.T) {
	a, _ := newHelpTestApp()

	_, cmd := a.Update(helpKey("q"))

	if a.showHelp {
		t.Fatal("q left the help overlay open")
	}
	if cmd != nil {
		t.Fatal("q quit the app instead of closing the help overlay")
	}
}

func TestHelpOverlayBlocksHiddenCommandMode(t *testing.T) {
	a, _ := newHelpTestApp()

	_, _ = a.Update(helpKey(":"))

	if !a.showHelp {
		t.Fatal(": unexpectedly closed the help overlay")
	}
	if a.cmdMode {
		t.Fatal(": opened command mode behind the help overlay")
	}
}

func TestHelpOverlayDoesNotForwardKeysToCurrentView(t *testing.T) {
	a, view := newHelpTestApp()

	_, _ = a.Update(helpKey("x"))

	if view.updates != 0 {
		t.Fatalf("hidden view received %d updates", view.updates)
	}
}

func newHelpTestApp() (*App, *helpTestView) {
	view := &helpTestView{}
	return &App{
		stack:    []views.Model{view},
		showHelp: true,
		cmdInput: newCmdInput(),
	}, view
}

func helpKey(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}
