package views

import (
	"sort"
	"strconv"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type ctxProbeMsg struct {
	name    string
	ok      bool
	version string
	jobs    int
	tms     int
}

// Contexts is the fleet view: every configured cluster with live
// reachability, so multi-environment operators see the whole estate at once.
type Contexts struct {
	deps   Deps
	tbl    *uitable.Model
	names  []string
	probes map[string]ctxProbeMsg
}

// NewContexts builds the fleet view.
func NewContexts(deps Deps) *Contexts {
	tbl := newTable([]uitable.Column{
		{Title: "CONTEXT", Width: 18},
		{Title: "URL", Width: -1},
		{Title: "STATUS", Width: 12},
		{Title: "FLINK", Width: 8},
		{Title: "JOBS", Width: 5},
		{Title: "TMS", Width: 4},
	})
	names := make([]string, 0, len(deps.Cfg.Contexts))
	for name := range deps.Cfg.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return &Contexts{deps: deps, tbl: tbl, names: names, probes: map[string]ctxProbeMsg{}}
}

func (c *Contexts) Title() string   { return "contexts" }
func (c *Contexts) Filtering() bool { return false }
func (c *Contexts) Bindings() []key.Binding {
	return []key.Binding{
		binding("enter", "enter", "switch to context"),
		binding("D", "D", "diff config vs active"),
		binding("esc", "esc", "back"),
	}
}

func (c *Contexts) Init() tea.Cmd { return c.Refresh() }

// Refresh probes every configured context in parallel.
func (c *Contexts) Refresh() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(c.names))
	for _, name := range c.names {
		name := name
		cctx := c.deps.Cfg.Contexts[name]
		cmds = append(cmds, func() tea.Msg {
			ov, err := client.New(cctx).Overview()
			if err != nil {
				return ctxProbeMsg{name: name, ok: false}
			}
			return ctxProbeMsg{name: name, ok: true, version: ov.FlinkVersion, jobs: ov.JobsRunning, tms: ov.TaskManagers}
		})
	}
	return tea.Batch(cmds...)
}

func (c *Contexts) rebuild() {
	rows := make([][]uitable.Cell, 0, len(c.names))
	for _, name := range c.names {
		cctx := c.deps.Cfg.Contexts[name]
		display := name
		if name == c.deps.Ctx {
			display = "* " + name
		}
		status := uitable.Styled("probing…", styles.Dim())
		version, jobs, tms := "-", "-", "-"
		if p, done := c.probes[name]; done {
			if p.ok {
				status = uitable.Styled("REACHABLE", styles.OKText())
				version = p.version
				jobs = strconv.Itoa(p.jobs)
				tms = strconv.Itoa(p.tms)
			} else {
				status = uitable.Styled("DOWN", styles.ErrText())
			}
		}
		rows = append(rows, []uitable.Cell{
			uitable.Styled(display, styles.Header()),
			uitable.Styled(cctx.URL, styles.Dim()),
			status,
			uitable.Plain(version),
			uitable.Plain(jobs),
			uitable.Plain(tms),
		})
	}
	c.tbl.SetRows(rows)
}

func (c *Contexts) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ctxProbeMsg:
		c.probes[msg.name] = msg
		c.rebuild()
		return c, nil
	case tea.KeyMsg:
		if msg.String() == "enter" && len(c.names) > 0 {
			name := c.names[c.tbl.Cursor()]
			return c, func() tea.Msg { return SwitchContextMsg{Name: name} }
		}
		if msg.String() == "D" && len(c.names) > 0 {
			other := c.names[c.tbl.Cursor()]
			if other == c.deps.Ctx {
				return c, flash("select a different context to diff against " + c.deps.Ctx)
			}
			return c, push(NewConfigDiff(c.deps, c.deps.Ctx, other))
		}
		c.tbl.Update(msg)
	}
	return c, nil
}

func (c *Contexts) View(width, height int) string {
	if c.tbl.Len() == 0 {
		c.rebuild()
	}
	out := styles.ViewTitle().Render("Contexts["+strconv.Itoa(len(c.names))+"]") +
		styles.Dim().Render("  * = active") + "\n"
	c.tbl.SetSize(width, height-1)
	return out + c.tbl.View()
}
