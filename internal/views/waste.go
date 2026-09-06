package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type wasteRow struct {
	name     string
	slots    int
	busyPct  int     // busy-weighted utilization %
	idleSlot float64 // slot-equivalents wasted
	costDay  float64 // $/day of idle slots (0 if no cost configured)
}

type wasteMsg struct {
	rows []wasteRow
	cost bool
	err  error
}

// WasteFinder ranks running jobs by busy-time-weighted slot waste — a job
// holding 4 slots at 6% busy is burning 3.8 idle slot-equivalents. The
// capacity companion to :pulse: where the money is going.
type WasteFinder struct {
	deps    Deps
	tbl     *uitable.Model
	filter  viewFilter
	hasCost bool
	err     string
}

// NewWasteFinder builds the view.
func NewWasteFinder(deps Deps) *WasteFinder {
	tbl := newTable([]uitable.Column{
		{Title: "JOB", Width: -1},
		{Title: "SLOTS", Width: 6, Right: true},
		{Title: "BUSY", Width: 6, Right: true},
		{Title: "IDLE SLOTS", Width: 11, Right: true},
		{Title: "$/DAY IDLE", Width: 11, Right: true},
	})
	return &WasteFinder{deps: deps, tbl: tbl, filter: newViewFilter()}
}

func (w *WasteFinder) Title() string   { return "waste" }
func (w *WasteFinder) Filtering() bool { return w.filter.Active() }
func (w *WasteFinder) Bindings() []key.Binding {
	return []key.Binding{binding("/", "/", "filter"), binding("r", "r", "refresh"), binding("esc", "esc", "back")}
}

func (w *WasteFinder) Init() tea.Cmd { return w.Refresh() }

func (w *WasteFinder) Refresh() tea.Cmd {
	cl := w.deps.CL
	cost := w.deps.Cfg.Contexts[w.deps.Ctx].CostPerSlotHour
	return func() tea.Msg {
		slots, err := cl.JobSlotCounts()
		if err != nil {
			return wasteMsg{err: err}
		}
		jobs, err := cl.Jobs()
		if err != nil {
			return wasteMsg{err: err}
		}
		var rows []wasteRow
		for _, j := range jobs {
			if j.State != "RUNNING" {
				continue
			}
			n := slots[j.JID]
			if n == 0 {
				continue
			}
			job, err := cl.Job(j.JID)
			if err != nil {
				continue
			}
			sum, cnt := 0.0, 0
			for _, v := range job.Vertices {
				if b, e := cl.VertexBusy(j.JID, v.ID); e == nil && b >= 0 {
					sum += b // busyTimeMsPerSecond (0..1000)
					cnt++
				}
			}
			busyFrac := 0.0
			if cnt > 0 {
				busyFrac = (sum / float64(cnt)) / 1000.0
			}
			idle := float64(n) * (1 - busyFrac)
			r := wasteRow{name: j.Name, slots: n, busyPct: int(busyFrac * 100), idleSlot: idle}
			if cost > 0 {
				r.costDay = idle * cost * 24
			}
			rows = append(rows, r)
		}
		sort.Slice(rows, func(a, b int) bool { return rows[a].idleSlot > rows[b].idleSlot })
		return wasteMsg{rows: rows, cost: cost > 0}
	}
}

func (w *WasteFinder) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case wasteMsg:
		if msg.err != nil {
			w.err = msg.err.Error()
			return w, nil
		}
		w.err = ""
		w.hasCost = msg.cost
		rows := make([][]uitable.Cell, 0, len(msg.rows))
		keys := make([]string, 0, len(msg.rows))
		for _, r := range msg.rows {
			keys = append(keys, r.name)
			busyStyle := styles.OKText()
			if r.busyPct < 20 && r.slots > 1 {
				busyStyle = styles.ErrText() // reclaim candidate
			} else if r.busyPct < 50 {
				busyStyle = styles.WarnText()
			}
			costCell := uitable.Styled("—", styles.Dim())
			if msg.cost {
				costCell = uitable.Styled(fmt.Sprintf("$%.2f", r.costDay), styles.WarnText())
			}
			rows = append(rows, []uitable.Cell{
				uitable.Plain(r.name),
				uitable.Plain(fmt.Sprint(r.slots)),
				uitable.Styled(fmt.Sprintf("%d%%", r.busyPct), busyStyle),
				uitable.Styled(fmt.Sprintf("%.1f", r.idleSlot), styles.Dim()),
				costCell,
			})
		}
		w.tbl.SetRowsKeyed(rows, keys)
		return w, nil
	case tea.KeyMsg:
		if handled, cmd := w.filter.Handle(msg, w.tbl); handled {
			return w, cmd
		}
		w.tbl.Update(msg)
	}
	return w, nil
}

func (w *WasteFinder) View(width, height int) string {
	sub := "reclaim candidates in red"
	if !w.hasCost {
		sub += " · set costPerSlotHour for $/day"
	}
	out := titleBar(width, "Waste finder", w.tbl.Len(), w.deps.Ctx+" · "+sub) + "\n"
	used := 1
	if fl := w.filter.Line(); fl != "" {
		out += fl
		used++
	}
	if w.err != "" {
		out += errorBanner(w.err)
		used++
	}
	if w.tbl.Len() == 0 && w.err == "" {
		return out + emptyState(" no running jobs holding slots")
	}
	w.tbl.SetSize(width, height-used)
	return out + w.tbl.View()
}

var _ = strings.TrimSpace
