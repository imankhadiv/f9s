package views

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aymanbagabas/go-osc52/v2"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type tmsMsg struct {
	tms []model.TaskManager
	err error
}

type tmHeapMsg struct {
	id      string
	heapPct int   // -1 unknown
	gcTotal int64 // cumulative GC ms across collectors; -1 unknown
}

// gcSample remembers the previous GC total so a rate can be computed.
type gcSample struct {
	total int64
	at    time.Time
	rate  float64 // ms of GC per wall second; -1 until two samples exist
}

// TaskManagersView lists the cluster's TaskManagers.
type TaskManagersView struct {
	deps     Deps
	tbl      *uitable.Model
	tms      []model.TaskManager
	heaps    map[string]tmHeapMsg
	gc       map[string]gcSample
	filter   viewFilter
	sortMode int
	err      string
}

// TM sort modes cycled by <o>.
const (
	tmSortID = iota
	tmSortHeap
	tmSortGC
	tmSortSlots
	tmSortModes
)

func tmSortLabel(mode int) string {
	switch mode {
	case tmSortHeap:
		return "heap↓"
	case tmSortGC:
		return "gc/s↓"
	case tmSortSlots:
		return "free-slots↑"
	}
	return "id"
}

// NewTaskManagers builds the TaskManager list view.
func NewTaskManagers(deps Deps) *TaskManagersView {
	tbl := newTable([]uitable.Column{
		{Title: "ID", Width: -1},
		{Title: "SLOTS", Width: 7, Right: true},
		{Title: "CPU", Width: 5, Right: true},
		{Title: "PHYS MEM", Width: 9, Right: true},
		{Title: "FREE MEM", Width: 9, Right: true},
		{Title: "HEAP", Width: 6, Right: true},
		{Title: "GC/s", Width: 6, Right: true},
		{Title: "HEARTBEAT", Width: 10, Right: true},
	})
	return &TaskManagersView{deps: deps, tbl: tbl, heaps: map[string]tmHeapMsg{}, gc: map[string]gcSample{}, filter: newViewFilter()}
}

func (t *TaskManagersView) Title() string   { return "taskmanagers" }
func (t *TaskManagersView) Filtering() bool { return t.filter.Active() }
func (t *TaskManagersView) Bindings() []key.Binding {
	return []key.Binding{
		binding("enter", "enter", "detail"),
		binding("l", "l", "logs"),
		binding("/", "/", "filter"),
		binding("y", "y", "copy id"),
		binding("o", "o", "sort"),
		binding("esc", "esc", "back"),
	}
}

func (t *TaskManagersView) Init() tea.Cmd { return t.Refresh() }

func (t *TaskManagersView) Refresh() tea.Cmd {
	cl := t.deps.CL
	return func() tea.Msg {
		tms, err := cl.TaskManagers()
		return tmsMsg{tms: tms, err: err}
	}
}

func (t *TaskManagersView) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tmHeapMsg:
		t.heaps[msg.id] = msg
		if msg.gcTotal >= 0 {
			now := time.Now()
			prev, seen := t.gc[msg.id]
			cur := gcSample{total: msg.gcTotal, at: now, rate: -1}
			if seen && now.After(prev.at) && msg.gcTotal >= prev.total {
				cur.rate = float64(msg.gcTotal-prev.total) / now.Sub(prev.at).Seconds()
			}
			t.gc[msg.id] = cur
		}
		t.rebuild()
		return t, nil

	case tmsMsg:
		if msg.err != nil {
			t.err = msg.err.Error()
			return t, nil
		}
		t.err = ""
		t.tms = msg.tms
		cl := t.deps.CL
		var cmds []tea.Cmd
		for _, tm := range t.tms {
			id := tm.ID
			cmds = append(cmds, func() tea.Msg {
				d, err := cl.TaskManager(id)
				if err != nil {
					return tmHeapMsg{id: id, heapPct: -1, gcTotal: -1}
				}
				var gcTotal int64
				for _, g := range d.Metrics.GarbageCollectors {
					gcTotal += g.Time
				}
				pct := -1
				if d.Metrics.HeapMax > 0 {
					pct = int(float64(d.Metrics.HeapUsed) / float64(d.Metrics.HeapMax) * 100)
				}
				return tmHeapMsg{id: id, heapPct: pct, gcTotal: gcTotal}
			})
		}
		t.rebuild()
		return t, tea.Batch(cmds...)
	case tea.KeyMsg:
		if handled, cmd := t.filter.Handle(msg, t.tbl); handled {
			return t, cmd
		}
		switch msg.String() {
		case "enter":
			if len(t.tms) > 0 {
				return t, push(NewTMDetail(t.deps, t.tms[t.tbl.Cursor()].ID))
			}
			return t, nil
		case "l":
			if len(t.tms) > 0 {
				return t, push(NewTMDetailLog(t.deps, t.tms[t.tbl.Cursor()].ID))
			}
			return t, nil
		case "o":
			t.sortMode = (t.sortMode + 1) % tmSortModes
			t.rebuild()
			return t, nil
		case "y":
			if len(t.tms) > 0 {
				id := t.tms[t.tbl.Cursor()].ID
				_, _ = osc52.New(id).WriteTo(os.Stderr)
				return t, flash("TaskManager id copied: " + id)
			}
			return t, nil
		}
		t.tbl.Update(msg)
	}
	return t, nil
}

func (t *TaskManagersView) rebuild() {
	switch t.sortMode {
	case tmSortHeap:
		sort.SliceStable(t.tms, func(a, b int) bool {
			return t.heaps[t.tms[a].ID].heapPct > t.heaps[t.tms[b].ID].heapPct
		})
	case tmSortGC:
		sort.SliceStable(t.tms, func(a, b int) bool {
			return t.gc[t.tms[a].ID].rate > t.gc[t.tms[b].ID].rate
		})
	case tmSortSlots:
		sort.SliceStable(t.tms, func(a, b int) bool {
			return t.tms[a].FreeSlots < t.tms[b].FreeSlots
		})
	default:
		sort.SliceStable(t.tms, func(a, b int) bool { return t.tms[a].ID < t.tms[b].ID })
	}
	if true {
		rows := make([][]uitable.Cell, 0, len(t.tms))
		keys := make([]string, 0, len(t.tms))
		for _, tm := range t.tms {
			keys = append(keys, tm.ID)
			slotStyle := styles.OKText()
			if tm.FreeSlots == 0 {
				slotStyle = styles.WarnText()
			}
			heap := uitable.Styled("…", styles.Dim())
			if h, ok := t.heaps[tm.ID]; ok {
				switch {
				case h.heapPct < 0:
					heap = uitable.Styled("-", styles.Dim())
				case h.heapPct >= 90:
					heap = uitable.Styled(fmt.Sprintf("%d%%", h.heapPct), styles.ErrText())
				case h.heapPct >= 75:
					heap = uitable.Styled(fmt.Sprintf("%d%%", h.heapPct), styles.WarnText())
				default:
					heap = uitable.Styled(fmt.Sprintf("%d%%", h.heapPct), styles.OKText())
				}
			}
			gcCell := uitable.Styled("…", styles.Dim())
			if g, ok := t.gc[tm.ID]; ok && g.rate >= 0 {
				text := fmt.Sprintf("%.0fms", g.rate)
				switch {
				case g.rate >= 200: // ≥20% of wall time in GC
					gcCell = uitable.Styled(text, styles.ErrText())
				case g.rate >= 50:
					gcCell = uitable.Styled(text, styles.WarnText())
				default:
					gcCell = uitable.Styled(text, styles.OKText())
				}
			}
			rows = append(rows, []uitable.Cell{
				uitable.Plain(tm.ID),
				uitable.Styled(fmt.Sprintf("%d/%d", tm.FreeSlots, tm.SlotsNumber), slotStyle),
				uitable.Plain(fmt.Sprintf("%.0f", tm.Hardware.CPUCores)),
				uitable.Plain(model.FmtBytes(tm.Hardware.PhysicalMemory)),
				uitable.Plain(model.FmtBytes(tm.Hardware.FreeMemory)),
				heap,
				gcCell,
				uitable.Styled(model.FmtAge(tm.TimeSinceLastHeartbeat), styles.Dim()),
			})
		}
		t.tbl.SetRowsKeyed(rows, keys)
	}
}

// AIContext summarizes the on-screen TaskManager fleet for the AI panel.
func (t *TaskManagersView) AIContext() (string, []string) {
	var b strings.Builder
	fmt.Fprintf(&b, "## On screen: %d TaskManagers\n", len(t.tms))
	if len(t.tms) == 0 {
		b.WriteString("no TaskManagers registered\n")
		return b.String(), nil
	}
	shown := 0
	for _, tm := range t.tms {
		if shown >= 12 {
			fmt.Fprintf(&b, "  … and %d more\n", len(t.tms)-shown)
			break
		}
		heap := "?"
		if h, ok := t.heaps[tm.ID]; ok && h.heapPct >= 0 {
			heap = fmt.Sprintf("%d%%", h.heapPct)
		}
		gc := "?"
		if g, ok := t.gc[tm.ID]; ok && g.rate >= 0 {
			gc = fmt.Sprintf("%.0fms/s", g.rate)
		}
		fmt.Fprintf(&b, "  %s: slots %d/%d free · heap %s · gc %s · heartbeat %s\n",
			tm.ID, tm.FreeSlots, tm.SlotsNumber, heap, gc, model.FmtAge(tm.TimeSinceLastHeartbeat))
		shown++
	}
	return b.String(), nil
}

func (t *TaskManagersView) View(width, height int) string {
	switch t.sortMode {
	case tmSortSlots:
		t.tbl.SetSort(1, styles.SortDesc)
	case tmSortHeap:
		t.tbl.SetSort(5, styles.SortDesc)
	case tmSortGC:
		t.tbl.SetSort(6, styles.SortDesc)
	default:
		t.tbl.SetSort(0, styles.SortDesc)
	}
	return framed(width, height, "TaskManagers", t.tbl.Len(), "sort: "+tmSortLabel(t.sortMode), func(iw, ih int) string {
		out := ""
		used := 0
		if fl := t.filter.Line(); fl != "" {
			out += fl
			used++
		}
		if t.err != "" {
			out += errorBanner(t.err)
			used++
		}
		if t.tbl.Len() == 0 {
			return out + emptyState("No TaskManagers registered — is the cluster healthy?")
		}
		t.tbl.SetSize(iw, ih-used)
		return out + t.tbl.View()
	})
}
