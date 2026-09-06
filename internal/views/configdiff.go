package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type configDiffMsg struct {
	rows [][3]string // key, valA, valB
	err  error
}

// ConfigDiff is the staging-vs-prod drift table: every cluster-config key
// that differs between two contexts, upgrade-critical keys highlighted.
type ConfigDiff struct {
	deps   Deps
	ctxA   string
	ctxB   string
	tbl    *uitable.Model
	filter viewFilter
	count  int
	err    string
}

// NewConfigDiff diffs ctxB (selected) against ctxA (active).
func NewConfigDiff(deps Deps, ctxA, ctxB string) *ConfigDiff {
	tbl := newTable([]uitable.Column{
		{Title: "KEY", Width: -1},
		{Title: strings.ToUpper(ctxA), Width: 34},
		{Title: strings.ToUpper(ctxB), Width: 34},
	})
	return &ConfigDiff{deps: deps, ctxA: ctxA, ctxB: ctxB, tbl: tbl, filter: newViewFilter()}
}

func (c *ConfigDiff) Title() string   { return "cfg diff" }
func (c *ConfigDiff) Filtering() bool { return c.filter.Active() }
func (c *ConfigDiff) Bindings() []key.Binding {
	return []key.Binding{
		binding("/", "/", "filter"),
		binding("esc", "esc", "back"),
	}
}

func (c *ConfigDiff) Init() tea.Cmd { return c.Refresh() }

// criticalKey flags settings that decide upgrade/incident behavior.
func criticalKey(k string) bool {
	for _, p := range []string{"checkpoint", "savepoint", "state.", "restart-strategy", "high-availability", "memory", "flink-version", "parallelism"} {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}

func (c *ConfigDiff) Refresh() tea.Cmd {
	cfg := c.deps.Cfg
	ctxA, ctxB := c.ctxA, c.ctxB
	clA := c.deps.CL
	return func() tea.Msg {
		fetch := func(cl *client.Client) (map[string]string, error) {
			entries, err := cl.JobManagerConfig()
			if err != nil {
				return nil, err
			}
			m := make(map[string]string, len(entries))
			for _, e := range entries {
				m[e.Key] = e.Value
			}
			return m, nil
		}
		a, err := fetch(clA)
		if err != nil {
			return configDiffMsg{err: fmt.Errorf("%s: %w", ctxA, err)}
		}
		b, err := fetch(client.New(cfg.Contexts[ctxB]))
		if err != nil {
			return configDiffMsg{err: fmt.Errorf("%s: %w", ctxB, err)}
		}
		keys := map[string]bool{}
		for k := range a {
			keys[k] = true
		}
		for k := range b {
			keys[k] = true
		}
		var rows [][3]string
		for k := range keys {
			va, vb := a[k], b[k]
			if va == vb {
				continue
			}
			if va == "" {
				va = "(unset)"
			}
			if vb == "" {
				vb = "(unset)"
			}
			rows = append(rows, [3]string{k, va, vb})
		}
		sort.Slice(rows, func(i, j int) bool {
			ci, cj := criticalKey(rows[i][0]), criticalKey(rows[j][0])
			if ci != cj {
				return ci
			}
			return rows[i][0] < rows[j][0]
		})
		return configDiffMsg{rows: rows}
	}
}

func (c *ConfigDiff) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case configDiffMsg:
		if msg.err != nil {
			c.err = msg.err.Error()
			return c, nil
		}
		c.err = ""
		c.count = len(msg.rows)
		rows := make([][]uitable.Cell, 0, len(msg.rows))
		keys := make([]string, 0, len(msg.rows))
		for _, r := range msg.rows {
			keys = append(keys, r[0])
			keyStyle := styles.Dim()
			if criticalKey(r[0]) {
				keyStyle = styles.WarnText()
			}
			rows = append(rows, []uitable.Cell{
				uitable.Styled(r[0], keyStyle),
				uitable.Plain(r[1]),
				uitable.Plain(r[2]),
			})
		}
		c.tbl.SetRowsKeyed(rows, keys)
		return c, nil
	case tea.KeyMsg:
		if handled, cmd := c.filter.Handle(msg, c.tbl); handled {
			return c, cmd
		}
		c.tbl.Update(msg)
	}
	return c, nil
}

func (c *ConfigDiff) View(width, height int) string {
	title := fmt.Sprintf("Config drift · %s vs %s", c.ctxA, c.ctxB)
	return framed(width, height, title, -1, "", func(iw, ih int) string {
		out := styles.Dim().Render(fmt.Sprintf("  %d differing keys · amber = upgrade-critical", c.count)) + "\n"
		used := 1
		if fl := c.filter.Line(); fl != "" {
			out += fl
			used++
		}
		if c.err != "" {
			out += styles.ErrText().Render("⚠ "+c.err) + "\n"
			used++
		}
		if c.count == 0 && c.err == "" {
			return out + styles.OKText().Render(" no drift — both clusters run identical configs")
		}
		c.tbl.SetSize(iw, ih-used)
		return out + c.tbl.View()
	})
}
