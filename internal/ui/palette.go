package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"

	"github.com/imankhadiv/f9s/internal/ui/styles"
)

// paletteItem is one executable entry: either a view navigation or a
// keypress replayed onto the current view.
type paletteItem struct {
	label string
	goTo  string // command-target canonical name; "" for key actions
	press tea.KeyMsg
}

// buildPalette assembles the current action space: the top view's own
// bindings (verbs) plus every command target (nouns).
func (a *App) buildPalette() []paletteItem {
	var items []paletteItem
	seen := map[string]bool{}
	for _, b := range a.top().Bindings() {
		keys := b.Keys()
		if len(keys) == 0 {
			continue
		}
		k := keys[0]
		msg, ok := keyMsgFor(k)
		if !ok {
			continue // esc/scroll keys don't belong in a palette
		}
		h := b.Help()
		label := h.Desc + "  (" + h.Key + ")"
		if seen[label] {
			continue
		}
		seen[label] = true
		items = append(items, paletteItem{label: label, press: msg})
	}
	for _, t := range commandTargets {
		if t.names[0] == "quit" {
			continue
		}
		items = append(items, paletteItem{label: "go to " + t.desc + "  (:" + t.names[0] + ")", goTo: t.names[0]})
	}
	return items
}

// keyMsgFor synthesizes the KeyMsg a binding's primary key would produce.
func keyMsgFor(k string) (tea.KeyMsg, bool) {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}, true
	case "ctrl+x":
		return tea.KeyMsg{Type: tea.KeyCtrlX}, true
	case "ctrl+p", "esc", "up", "down", "pgup", "pgdown", "tab", "left", "right", "/":
		return tea.KeyMsg{}, false
	}
	runes := []rune(k)
	if len(runes) != 1 {
		return tea.KeyMsg{}, false
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: runes}, true
}

// matchPalette filters items by the query.
func matchPalette(items []paletteItem, q string) []paletteItem {
	if q == "" {
		return items
	}
	targets := make([]string, len(items))
	for i, it := range items {
		targets[i] = it.label
	}
	var out []paletteItem
	for _, m := range fuzzy.Find(q, targets) {
		out = append(out, items[m.Index])
	}
	return out
}

// handlePaletteKey drives palette mode; returns the follow-up command.
func (a *App) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+p":
		a.paletteMode = false
		a.paletteInput.Blur()
		return a, nil
	case "up":
		if a.paletteSel > 0 {
			a.paletteSel--
		}
		return a, nil
	case "down":
		a.paletteSel++
		return a, nil
	case "enter":
		matches := matchPalette(a.paletteItems, a.paletteInput.Value())
		if len(matches) == 0 {
			return a, nil
		}
		if a.paletteSel >= len(matches) {
			a.paletteSel = len(matches) - 1
		}
		it := matches[a.paletteSel]
		a.paletteMode = false
		a.paletteInput.Blur()
		if it.goTo != "" {
			return a, a.runCommand(it.goTo)
		}
		// replay the keypress onto the current view
		next, cmd := a.top().Update(it.press)
		a.stack[len(a.stack)-1] = next
		return a, cmd
	}
	var cmd tea.Cmd
	a.paletteInput, cmd = a.paletteInput.Update(msg)
	a.paletteSel = 0
	return a, cmd
}

// paletteView renders the overlay list.
func (a *App) paletteView(height int) string {
	var b strings.Builder
	b.WriteString(" " + a.paletteInput.View() + "\n")
	matches := matchPalette(a.paletteItems, a.paletteInput.Value())
	if a.paletteSel >= len(matches) && len(matches) > 0 {
		a.paletteSel = len(matches) - 1
	}
	max := height - 3
	for i, it := range matches {
		if i >= max {
			b.WriteString(styles.Dim().Render("  …") + "\n")
			break
		}
		prefix := "  "
		line := it.label
		if i == a.paletteSel {
			prefix = styles.Header().Render("❯ ")
			b.WriteString(prefix + line + "\n")
			continue
		}
		b.WriteString(prefix + styles.Dim().Render(line) + "\n")
	}
	if len(matches) == 0 {
		b.WriteString(styles.Dim().Render("  nothing matches"))
	}
	return b.String()
}

var _ = key.Binding{} // keep the import stable across edits
