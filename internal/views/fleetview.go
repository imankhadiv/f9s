package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/fleet"
	"github.com/imankhadiv/f9s/internal/metricts"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type fleetCollectedMsg struct{}

// FleetView is the JVM fleet at a glance: the JobManager and every
// TaskManager with heap, GC time, metaspace, managed memory and network
// buffers, trended from the metric ring the sampler fills every 15s.
type FleetView struct {
	deps    Deps
	tbl     *uitable.Model
	rows    []fleet.Row
	now     func() time.Time
	primed  bool
	collect func() // one-off sample when the ring is still empty
}

// NewFleet builds the fleet view.
func NewFleet(deps Deps) *FleetView {
	tbl := newTable([]uitable.Column{
		{Title: "JVM", Width: -1},
		{Title: "HEAP", Width: 5, Right: true},
		{Title: "HEAP 5m", Width: 20},
		{Title: "GC", Width: 6, Right: true},
		{Title: "GC 5m", Width: 20},
		{Title: "META", Width: 5, Right: true},
		{Title: "MANAGED", Width: 7, Right: true},
		{Title: "NET BUF", Width: 7, Right: true},
		{Title: "CPU", Width: 5, Right: true},
		{Title: "FLAGS", Width: 36},
	})
	v := &FleetView{deps: deps, tbl: tbl, now: time.Now}
	v.collect = func() {
		if deps.CL != nil {
			fleet.Collect(deps.CL, deps.Metrics, time.Now())
		}
	}
	return v
}

func (f *FleetView) Title() string   { return "fleet" }
func (f *FleetView) Filtering() bool { return false }
func (f *FleetView) Bindings() []key.Binding {
	return []key.Binding{binding("enter", "enter", "TaskManager detail"), binding("esc", "esc", "back")}
}

// Init samples once right away when the ring has no JVM history yet, so the
// view isn't empty for the first 15 seconds.
func (f *FleetView) Init() tea.Cmd {
	f.rebuild()
	if len(f.rows) > 0 || f.primed {
		return nil
	}
	f.primed = true
	collect := f.collect
	return func() tea.Msg {
		collect()
		return fleetCollectedMsg{}
	}
}

// Refresh re-reads the ring (no REST calls: the sampler owns collection).
func (f *FleetView) Refresh() tea.Cmd {
	f.rebuild()
	return nil
}

func pctCell(frac, warn, crit float64) uitable.Cell {
	if frac < 0 {
		return uitable.Styled("-", styles.Dim())
	}
	return uitable.Styled(fmt.Sprintf("%.0f%%", frac*100), styles.Severity(frac, warn, crit))
}

// tail keeps the newest n samples (sparkline width).
func tailN(vals []float64, n int) []float64 {
	if len(vals) > n {
		return vals[len(vals)-n:]
	}
	return vals
}

func (f *FleetView) rebuild() {
	f.rows = fleet.Rows(f.deps.Metrics, f.now())
	rows := make([][]uitable.Cell, 0, len(f.rows))
	keys := make([]string, 0, len(f.rows))
	for _, r := range f.rows {
		name := r.ID
		if r.Scope == metricts.ScopeJM {
			name = "jobmanager"
		}
		gc := uitable.Styled("-", styles.Dim())
		if r.GCMsPerSec >= 0 {
			gc = uitable.Styled(fmt.Sprintf("%.0f%%", r.GCMsPerSec/10), styles.Severity(r.GCMsPerSec/1000, 0.1, 0.3))
		}
		cpu := uitable.Styled("-", styles.Dim())
		if r.CPU >= 0 {
			cpu = uitable.Plain(fmt.Sprintf("%.0f%%", r.CPU*100))
		}
		flags := uitable.Styled("", styles.Dim())
		if len(r.Verdicts) > 0 {
			texts := make([]string, len(r.Verdicts))
			st := styles.WarnText()
			for i, v := range r.Verdicts {
				texts[i] = v.Text
				if v.Level == "crit" {
					st = styles.ErrText()
				}
			}
			flags = uitable.Styled("⚑ "+strings.Join(texts, " · "), st)
		}
		rows = append(rows, []uitable.Cell{
			uitable.Plain(name),
			pctCell(r.Heap, 0.75, 0.9),
			uitable.Styled(styles.Spark(tailN(r.HeapTrend, 20)), styles.Dim()),
			gc,
			uitable.Styled(styles.Spark(tailN(r.GCTrend, 20)), styles.Dim()),
			pctCell(r.Meta, 0.8, 0.9),
			pctCell(r.Managed, 0.9, 0.98),
			pctCell(r.Network, 0.8, 0.95),
			cpu,
			flags,
		})
		keys = append(keys, r.Scope+"/"+r.ID)
	}
	f.tbl.SetRowsKeyed(rows, keys)
}

func (f *FleetView) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case fleetCollectedMsg:
		f.rebuild()
		return f, nil
	case tea.KeyMsg:
		if msg.String() == "enter" && f.tbl.Len() > 0 {
			if i := f.tbl.Cursor(); i >= 0 && i < len(f.rows) && f.rows[i].Scope == metricts.ScopeTM {
				return f, push(NewTMDetail(f.deps, f.rows[i].ID))
			}
			return f, nil
		}
		f.tbl.Update(msg)
	}
	return f, nil
}

// AIContext hands the AI panel the fleet's resource picture and flags.
func (f *FleetView) AIContext() (string, []string) {
	var b strings.Builder
	fmt.Fprintf(&b, "## On screen: JVM fleet (%d JVMs, trends over %s)\n", len(f.rows), fleet.Window)
	pct := func(v float64) string {
		if v < 0 {
			return "?"
		}
		return fmt.Sprintf("%.0f%%", v*100)
	}
	for i, r := range f.rows {
		if i >= 20 {
			fmt.Fprintf(&b, "  … and %d more\n", len(f.rows)-i)
			break
		}
		gc := "?"
		if r.GCMsPerSec >= 0 {
			gc = fmt.Sprintf("%.0f%% of wall time", r.GCMsPerSec/10)
		}
		fmt.Fprintf(&b, "  %s %s: heap %s · gc %s · metaspace %s · managed %s · network buffers %s",
			r.Scope, r.ID, pct(r.Heap), gc, pct(r.Meta), pct(r.Managed), pct(r.Network))
		for _, v := range r.Verdicts {
			fmt.Fprintf(&b, " · [%s] %s", v.Level, v.Text)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func (f *FleetView) View(width, height int) string {
	flagged := 0
	for _, r := range f.rows {
		if len(r.Verdicts) > 0 {
			flagged++
		}
	}
	right := fmt.Sprintf("%d flagged · sampled every 15s", flagged)
	return framed(width, height, "Fleet", f.tbl.Len(), right, func(iw, ih int) string {
		if f.tbl.Len() == 0 {
			return emptyState(" collecting JVM metrics… (the sampler fills this every 15s)")
		}
		out := styles.Dim().Render(" GC = share of wall time spent collecting (5m) · NET BUF = network memory segments in use") + "\n"
		f.tbl.SetSize(iw, ih-1)
		return out + f.tbl.View()
	})
}
