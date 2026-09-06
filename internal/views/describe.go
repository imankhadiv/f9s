package views

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ui/styles"
)

// Describe renders any entity as pretty-printed, lightly syntax-highlighted
// JSON in a scrollable framed pager — the k9s `y`/describe muscle-memory verb,
// for pasting into a ticket or eyeballing a field the tables don't show.
type Describe struct {
	deps  Deps
	title string
	text  string
	vp    viewport.Model
	ready bool
}

// NewDescribe marshals v to indented JSON and highlights it.
func NewDescribe(deps Deps, title string, v any) *Describe {
	body, err := json.MarshalIndent(v, "", "  ")
	text := ""
	if err != nil {
		text = styles.ErrText().Render("cannot render: " + err.Error())
	} else {
		text = highlightJSON(string(body))
	}
	return &Describe{deps: deps, title: title, text: text}
}

func (d *Describe) Title() string   { return "describe" }
func (d *Describe) Filtering() bool { return false }
func (d *Describe) Bindings() []key.Binding {
	return []key.Binding{
		binding("up", "↑↓/pgup", "scroll"),
		binding("esc", "esc", "back"),
	}
}
func (d *Describe) Init() tea.Cmd    { return nil }
func (d *Describe) Refresh() tea.Cmd { return nil }

func (d *Describe) Update(msg tea.Msg) (Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok && d.ready {
		var cmd tea.Cmd
		d.vp, cmd = d.vp.Update(msg)
		return d, cmd
	}
	return d, nil
}

func (d *Describe) View(width, height int) string {
	return framed(width, height, d.title, -1, "describe · JSON", func(iw, ih int) string {
		if !d.ready {
			d.vp = viewport.New(iw, ih)
			d.vp.SetContent(d.text)
			d.ready = true
		}
		d.vp.Width = iw
		d.vp.Height = ih
		return d.vp.View()
	})
}

var jsonKeyRe = regexp.MustCompile(`^(\s*)("(?:[^"\\]|\\.)*")(\s*:\s*)(.*)$`)

// highlightJSON applies light color: keys in accent, string values green,
// everything else plain. Cheap and line-based — not a full lexer.
func highlightJSON(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		m := jsonKeyRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		indent, k, colon, val := m[1], m[2], m[3], m[4]
		if strings.HasPrefix(val, `"`) {
			// split a trailing comma off the string value so it stays plain
			comma := ""
			if strings.HasSuffix(val, ",") {
				val, comma = strings.TrimSuffix(val, ","), ","
			}
			val = styles.OKText().Render(val) + comma
		}
		lines[i] = indent + styles.Accent().Render(k) + colon + val
	}
	return strings.Join(lines, "\n")
}
