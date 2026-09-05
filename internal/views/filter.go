package views

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

// viewFilter is the shared '/' row filter: any list view embeds one, calls
// Handle first in its key handling, and renders Line() when active.
type viewFilter struct {
	input textinput.Model
	on    bool
}

func newViewFilter() viewFilter {
	in := textinput.New()
	in.Prompt = "/"
	in.CharLimit = 64
	return viewFilter{input: in}
}

// Handle consumes filter-related keys. It returns (true, cmd) when the key
// belonged to the filter; the view should return immediately then.
func (f *viewFilter) Handle(msg tea.KeyMsg, tbl *uitable.Model) (bool, tea.Cmd) {
	if f.on {
		switch msg.String() {
		case "esc":
			f.input.SetValue("")
			tbl.SetFilter("")
			f.on = false
			f.input.Blur()
			return true, nil
		case "enter":
			f.on = false
			f.input.Blur()
			return true, nil
		}
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		tbl.SetFilter(f.input.Value())
		return true, cmd
	}
	if msg.String() == "/" {
		f.on = true
		return true, f.input.Focus()
	}
	return false, nil
}

// Line renders the filter bar while typing or while a filter is applied;
// empty otherwise.
func (f *viewFilter) Line() string {
	if !f.on && f.input.Value() == "" {
		return ""
	}
	suffix := ""
	if !f.on {
		suffix = styles.Dim().Render("  (enter to edit — esc clears)")
	}
	return " " + f.input.View() + suffix + "\n"
}

// Active reports whether the filter owns the keyboard.
func (f *viewFilter) Active() bool { return f.on }
