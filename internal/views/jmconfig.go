package views

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type jmConfigMsg struct {
	entries []model.ConfigEntry
	err     error
}

// JMConfig browses the cluster's effective configuration with a filter —
// where you check jobmanager.scheduler, state backend, and friends.
type JMConfig struct {
	deps     Deps
	tbl      *uitable.Model
	entries  []model.ConfigEntry
	filter   textinput.Model
	filterOn bool
	err      string
}

// NewJMConfig builds the config browser.
func NewJMConfig(deps Deps) *JMConfig {
	tbl := newTable([]uitable.Column{
		{Title: "KEY", Width: 46},
		{Title: "VALUE", Width: -1},
	})
	fi := textinput.New()
	fi.Prompt = "/"
	fi.CharLimit = 64
	return &JMConfig{deps: deps, tbl: tbl, filter: fi}
}

func (c *JMConfig) Title() string   { return "config" }
func (c *JMConfig) Filtering() bool { return c.filterOn }
func (c *JMConfig) Bindings() []key.Binding {
	return []key.Binding{binding("/", "/", "filter"), binding("esc", "esc", "back")}
}

func (c *JMConfig) Init() tea.Cmd { return c.Refresh() }

func (c *JMConfig) Refresh() tea.Cmd {
	cl := c.deps.CL
	return func() tea.Msg {
		entries, err := cl.JobManagerConfig()
		return jmConfigMsg{entries: entries, err: err}
	}
}

func (c *JMConfig) rebuild() {
	needle := strings.ToLower(c.filter.Value())
	rows := make([][]uitable.Cell, 0, len(c.entries))
	for _, e := range c.entries {
		if needle != "" && !strings.Contains(strings.ToLower(e.Key), needle) &&
			!strings.Contains(strings.ToLower(e.Value), needle) {
			continue
		}
		rows = append(rows, []uitable.Cell{
			uitable.Plain(e.Key),
			uitable.Styled(e.Value, styles.Dim()),
		})
	}
	c.tbl.SetRows(rows)
}

func (c *JMConfig) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case jmConfigMsg:
		if msg.err != nil {
			c.err = msg.err.Error()
			return c, nil
		}
		c.err = ""
		c.entries = msg.entries
		c.rebuild()
		return c, nil
	case tea.KeyMsg:
		if c.filterOn {
			switch msg.String() {
			case "enter", "esc":
				if msg.String() == "esc" {
					c.filter.SetValue("")
				}
				c.filterOn = false
				c.filter.Blur()
				c.rebuild()
				return c, nil
			}
			var cmd tea.Cmd
			c.filter, cmd = c.filter.Update(msg)
			c.rebuild()
			return c, cmd
		}
		if msg.String() == "/" {
			c.filterOn = true
			return c, c.filter.Focus()
		}
		c.tbl.Update(msg)
	}
	return c, nil
}

func (c *JMConfig) View(width, height int) string {
	out := styles.ViewTitle().Render("Cluster config["+strconv.Itoa(c.tbl.Len())+"]") + "\n"
	used := 1
	if c.filterOn || c.filter.Value() != "" {
		out += c.filter.View() + "\n"
		used++
	}
	if c.err != "" {
		out += styles.ErrText().Render(" ⚠ "+c.err) + "\n"
		used++
	}
	c.tbl.SetSize(width, height-used)
	return out + c.tbl.View()
}
