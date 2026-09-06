package views

import (
	"fmt"
	"net/url"
	"os"
	"sort"

	"github.com/aymanbagabas/go-osc52/v2"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

// ClusterView is a single selectable list of cluster components — the
// JobManager on the top row, then every TaskManager. Enter drills into the
// selected component; the Flink Web UI splits these across two pages, we
// unify them so one keystroke reaches either.
type ClusterView struct {
	deps   Deps
	tbl    *uitable.Model
	tms    []model.TaskManager
	jm     model.JMMetrics
	jmOK   bool // JM metrics fetched at least once
	jmErr  bool
	stats  map[string]compStat // keyed by tm id
	filter viewFilter
	err    string
}

// compStat is the lazily-fetched per-TaskManager JVM snapshot.
type compStat struct {
	heapPct int // -1 unknown
	cpuPct  int // -1 unknown
}

type clusterTMsMsg struct {
	tms []model.TaskManager
	err error
}
type clusterJMMsg struct {
	jm  model.JMMetrics
	err error
}
type clusterStatMsg struct {
	id   string
	stat compStat
}

// NewCluster builds the unified cluster-components view.
func NewCluster(deps Deps) *ClusterView {
	tbl := newTable([]uitable.Column{
		{Title: "COMPONENT", Width: 12},
		{Title: "ADDRESS", Width: -1},
		{Title: "SLOTS", Width: 7, Right: true},
		{Title: "CPU", Width: 5, Right: true},
		{Title: "HEAP", Width: 6, Right: true},
		{Title: "STATUS", Width: 12},
	})
	return &ClusterView{deps: deps, tbl: tbl, stats: map[string]compStat{}, filter: newViewFilter()}
}

func (c *ClusterView) Title() string   { return "cluster" }
func (c *ClusterView) Filtering() bool { return c.filter.Active() }

func (c *ClusterView) Bindings() []key.Binding {
	return []key.Binding{
		binding("enter", "enter", "detail"),
		binding("l", "l", "logs"),
		binding("c", "c", "config"),
		binding("/", "/", "filter"),
		binding("y", "y", "copy address"),
		binding("esc", "esc", "back"),
	}
}

func (c *ClusterView) Init() tea.Cmd { return c.Refresh() }

func (c *ClusterView) Refresh() tea.Cmd {
	cl := c.deps.CL
	return tea.Batch(
		func() tea.Msg {
			tms, err := cl.TaskManagers()
			return clusterTMsMsg{tms: tms, err: err}
		},
		func() tea.Msg {
			jm, err := cl.JobManagerMetrics()
			return clusterJMMsg{jm: jm, err: err}
		},
	)
}

// jmAddress derives a display host for the JobManager from the context URL.
func (c *ClusterView) jmAddress() string {
	ctx, ok := c.deps.Cfg.Contexts[c.deps.Ctx]
	if !ok || ctx.URL == "" {
		return "—"
	}
	if u, err := url.Parse(ctx.URL); err == nil && u.Host != "" {
		return u.Host
	}
	return ctx.URL
}

func (c *ClusterView) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case clusterJMMsg:
		c.jmOK = true
		c.jmErr = msg.err != nil
		if msg.err == nil {
			c.jm = msg.jm
		}
		c.rebuild()
		return c, nil

	case clusterStatMsg:
		c.stats[msg.id] = msg.stat
		c.rebuild()
		return c, nil

	case clusterTMsMsg:
		if msg.err != nil {
			c.err = msg.err.Error()
			return c, nil
		}
		c.err = ""
		c.tms = msg.tms
		cl := c.deps.CL
		var cmds []tea.Cmd
		for _, tm := range c.tms {
			id := tm.ID
			cmds = append(cmds, func() tea.Msg {
				stat := compStat{heapPct: -1, cpuPct: -1}
				if d, err := cl.TaskManager(id); err == nil && d.Metrics.HeapMax > 0 {
					stat.heapPct = int(float64(d.Metrics.HeapUsed) / float64(d.Metrics.HeapMax) * 100)
				}
				if ms, err := cl.MetricValues("/taskmanagers/"+id+"/metrics", []string{"Status.JVM.CPU.Load"}); err == nil {
					for _, m := range ms {
						if v, ok := m.Float(); ok {
							stat.cpuPct = int(v * 100)
						}
					}
				}
				return clusterStatMsg{id: id, stat: stat}
			})
		}
		c.rebuild()
		return c, tea.Batch(cmds...)

	case tea.KeyMsg:
		if handled, cmd := c.filter.Handle(msg, c.tbl); handled {
			return c, cmd
		}
		switch msg.String() {
		case "enter":
			if c.onJM() {
				return c, push(NewJMConfig(c.deps))
			}
			if tm, ok := c.selectedTM(); ok {
				return c, push(NewTMDetail(c.deps, tm.ID))
			}
		case "l":
			if c.onJM() {
				return c, push(NewJMLog(c.deps))
			}
			if tm, ok := c.selectedTM(); ok {
				return c, push(NewTMDetailLog(c.deps, tm.ID))
			}
		case "c":
			if c.onJM() {
				return c, push(NewJMConfig(c.deps))
			}
			return c, flash("no config page for a TaskManager — enter opens its detail")
		case "y":
			addr := c.jmAddress()
			if tm, ok := c.selectedTM(); ok {
				addr = tm.ID
			}
			_, _ = osc52.New(addr).WriteTo(os.Stderr)
			return c, flash("address copied: " + addr)
		}
		c.tbl.Update(msg)
	}
	return c, nil
}

// onJM reports whether the cursor is on the JobManager (always row 0).
func (c *ClusterView) onJM() bool { return c.tbl.Cursor() == 0 }

// selectedTM returns the TaskManager under the cursor (cursor 0 is the JM).
func (c *ClusterView) selectedTM() (model.TaskManager, bool) {
	i := c.tbl.Cursor() - 1
	if i < 0 || i >= len(c.tms) {
		return model.TaskManager{}, false
	}
	return c.tms[i], true
}

// heapCell renders a heap percentage with a severity colour.
func heapCell(pct int) uitable.Cell {
	switch {
	case pct < 0:
		return uitable.Styled("…", styles.Dim())
	case pct >= 90:
		return uitable.Styled(fmt.Sprintf("%d%%", pct), styles.ErrText())
	case pct >= 75:
		return uitable.Styled(fmt.Sprintf("%d%%", pct), styles.WarnText())
	default:
		return uitable.Styled(fmt.Sprintf("%d%%", pct), styles.OKText())
	}
}

func cpuCell(pct int) uitable.Cell {
	if pct < 0 {
		return uitable.Styled("…", styles.Dim())
	}
	return uitable.Plain(fmt.Sprintf("%d%%", pct))
}

func (c *ClusterView) rebuild() {
	sort.SliceStable(c.tms, func(a, b int) bool { return c.tms[a].ID < c.tms[b].ID })

	rows := make([][]uitable.Cell, 0, len(c.tms)+1)
	keys := make([]string, 0, len(c.tms)+1)

	// Row 0: the JobManager.
	jmHeap := uitable.Styled("…", styles.Dim())
	jmCPU := uitable.Styled("…", styles.Dim())
	jmStatus := uitable.Styled("● HEALTHY", styles.OKText())
	if c.jmOK {
		if c.jmErr {
			jmStatus = uitable.Styled("⚠ UNREACHABLE", styles.ErrText())
		} else {
			jmHeap = heapCell(c.jm.HeapPct())
			if c.jm.CPULoad >= 0 {
				jmCPU = cpuCell(int(c.jm.CPULoad * 100))
			}
			if c.jm.HeapPct() >= 90 {
				jmStatus = uitable.Styled("⚠ HEAP", styles.ErrText())
			}
		}
	}
	rows = append(rows, []uitable.Cell{
		uitable.Styled("JobManager", styles.OKText()),
		uitable.Plain(c.jmAddress()),
		uitable.Styled("—", styles.Dim()),
		jmCPU,
		jmHeap,
		jmStatus,
	})
	keys = append(keys, "__jm__")

	// TaskManager rows.
	for _, tm := range c.tms {
		st, ok := c.stats[tm.ID]
		if !ok {
			st = compStat{heapPct: -1, cpuPct: -1}
		}
		slotStyle := styles.OKText()
		if tm.FreeSlots == 0 {
			slotStyle = styles.WarnText()
		}
		status := uitable.Styled("● HEALTHY", styles.OKText())
		switch {
		case st.heapPct >= 90:
			status = uitable.Styled("⚠ HEAP", styles.ErrText())
		case tm.SlotsNumber > 0 && tm.FreeSlots == tm.SlotsNumber:
			status = uitable.Styled("○ IDLE", styles.Dim())
		case tm.FreeSlots == 0:
			status = uitable.Styled("⚠ FULL", styles.WarnText())
		}
		rows = append(rows, []uitable.Cell{
			uitable.Plain("TaskManager"),
			uitable.Plain(tm.ID),
			uitable.Styled(fmt.Sprintf("%d/%d", tm.SlotsNumber-tm.FreeSlots, tm.SlotsNumber), slotStyle),
			cpuCell(st.cpuPct),
			heapCell(st.heapPct),
			status,
		})
		keys = append(keys, tm.ID)
	}
	c.tbl.SetRowsKeyed(rows, keys)
}

func (c *ClusterView) View(width, height int) string {
	out := titleBar(width, "Cluster", c.tbl.Len(), "JobManager + TaskManagers") + "\n"
	used := 1
	if fl := c.filter.Line(); fl != "" {
		out += fl
		used++
	}
	if c.err != "" {
		out += errorBanner(c.err)
		used++
	}
	c.tbl.SetSize(width, height-used)
	return out + c.tbl.View()
}
