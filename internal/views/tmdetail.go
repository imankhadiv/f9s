package views

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type tmDetailMsg struct {
	tm   model.TMDetail
	live model.TMMemoryLive
	jobs map[string]string // jid → name, for slot occupancy
	err  error
}

type tmLogMsg struct {
	text string
	err  error
}

type profileDoneMsg struct {
	path string
	err  error
}

// TMDetail shows one TaskManager: heap, GC totals, and its log tail.
type TMDetail struct {
	deps    Deps
	id      string
	tm      model.TMDetail
	live    model.TMMemoryLive
	jobs    map[string]string
	showLog bool
	logText string
	isDump  bool // viewport currently holds a thread dump, not the log
	vp      viewport.Model
	ready   bool
	err     string
}

// NewTMDetail builds the detail view for one TaskManager.
func NewTMDetail(deps Deps, id string) *TMDetail {
	return &TMDetail{deps: deps, id: id}
}

// NewTMDetailLog opens the detail with the log already tailing — the
// k9s-style "l straight from the list" path.
func NewTMDetailLog(deps Deps, id string) *TMDetail {
	return &TMDetail{deps: deps, id: id, showLog: true}
}

func (t *TMDetail) Title() string   { return t.id }
func (t *TMDetail) Filtering() bool { return false }
func (t *TMDetail) Bindings() []key.Binding {
	return []key.Binding{
		binding("P", "P", "flame graph (15s)"),
		binding("l", "l", "toggle log"),
		binding("T", "T", "thread dump"),
		binding("F", "F", "log files"),
		binding("o", "o", "stdout"),
		binding("up", "↑↓/pgup", "scroll log"),
		binding("esc", "esc", "back"),
	}
}

func (t *TMDetail) Init() tea.Cmd {
	if t.showLog {
		return tea.Batch(t.Refresh(), t.fetchLog(), flash("fetching TaskManager log…"))
	}
	return t.Refresh()
}

func (t *TMDetail) Refresh() tea.Cmd {
	cl := t.deps.CL
	id := t.id
	return func() tea.Msg {
		tm, err := cl.TaskManager(id)
		if err != nil {
			return tmDetailMsg{err: err}
		}
		live, _ := cl.TMMemoryLive(id)
		names := map[string]string{}
		if jobs, err := cl.Jobs(); err == nil {
			for _, j := range jobs {
				names[j.JID] = j.Name
			}
		}
		return tmDetailMsg{tm: tm, live: live, jobs: names}
	}
}

func (t *TMDetail) fetchLog() tea.Cmd {
	cl := t.deps.CL
	id := t.id
	return func() tea.Msg {
		text, err := cl.TMLog(id)
		return tmLogMsg{text: text, err: err}
	}
}

// tail keeps the last n lines of s.
func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (t *TMDetail) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tmDetailMsg:
		if msg.err != nil {
			t.err = msg.err.Error()
			return t, nil
		}
		t.err = ""
		t.tm = msg.tm
		t.live = msg.live
		t.jobs = msg.jobs
		return t, nil

	case tmLogMsg:
		if msg.err != nil {
			t.err = msg.err.Error()
			return t, nil
		}
		t.logText = tail(msg.text, 500)
		if t.ready {
			t.vp.SetContent(t.logText)
			t.vp.GotoBottom()
		}
		return t, nil

	case profileDoneMsg:
		if msg.err != nil {
			return t, flashErrV(msg.err.Error())
		}
		openInBrowser(msg.path)
		return t, flash("flame graph opened: " + msg.path)

	case tea.KeyMsg:
		if msg.String() == "P" {
			cl := t.deps.CL
			id := t.id
			return t, tea.Batch(
				flash("profiling "+id+" for 15s (ITIMER) — the flame graph opens when ready"),
				func() tea.Msg {
					if _, err := cl.StartTMProfiler(id, "ITIMER", 15); err != nil {
						return profileDoneMsg{err: fmt.Errorf("start profiler: %w (is rest.profiling.enabled: true?)", err)}
					}
					time.Sleep(18 * time.Second)
					list, err := cl.ListTMProfiles(id)
					if err != nil || len(list) == 0 {
						return profileDoneMsg{err: fmt.Errorf("profiler list: %v", err)}
					}
					newest := list[0]
					if newest.Status != "FINISHED" || newest.OutputFile == "" {
						return profileDoneMsg{err: fmt.Errorf("profiling %s: %s", newest.Status, newest.Message)}
					}
					html, err := cl.DownloadTMProfile(id, newest.OutputFile)
					if err != nil {
						return profileDoneMsg{err: err}
					}
					dir := filepath.Join(os.TempDir(), "f9s-profiles")
					if err := os.MkdirAll(dir, 0o755); err != nil {
						return profileDoneMsg{err: err}
					}
					path := filepath.Join(dir, newest.OutputFile)
					if err := os.WriteFile(path, []byte(html), 0o644); err != nil {
						return profileDoneMsg{err: err}
					}
					return profileDoneMsg{path: path}
				})
		}
		if msg.String() == "l" {
			if t.isDump {
				t.isDump = false
				t.logText = ""
			}
			t.showLog = !t.showLog
			if t.showLog && t.logText == "" {
				return t, tea.Batch(t.fetchLog(), flash("fetching TaskManager log…"))
			}
			return t, nil
		}
		if msg.String() == "F" {
			return t, push(NewTMLogFiles(t.deps, t.id))
		}
		if msg.String() == "o" {
			cl := t.deps.CL
			id := t.id
			return t, push(NewTextPager(t.deps, "stdout("+id+")", func() (string, error) { return cl.TMStdout(id) }))
		}
		if msg.String() == "T" {
			cl := t.deps.CL
			id := t.id
			t.showLog = true
			t.isDump = true
			t.logText = ""
			return t, tea.Batch(flash("capturing thread dump…"), func() tea.Msg {
				dump, err := cl.TMThreadDump(id)
				return tmLogMsg{text: dump, err: err}
			})
		}
		if t.showLog {
			var cmd tea.Cmd
			t.vp, cmd = t.vp.Update(msg)
			return t, cmd
		}
	}
	return t, nil
}

// heapLine renders heap usage with severity coloring.
func heapLine(used, max int64) string {
	if max <= 0 {
		return styles.Dim().Render("heap: -")
	}
	pct := int(float64(used) / float64(max) * 100)
	text := fmt.Sprintf("heap %s / %s (%d%%)", model.FmtBytes(used), model.FmtBytes(max), pct)
	switch {
	case pct >= 90:
		return styles.ErrText().Render(text)
	case pct >= 75:
		return styles.WarnText().Render(text)
	default:
		return styles.OKText().Render(text)
	}
}

// memRow renders one memory-model bar: label, gauge, pct, used/total.
func memRow(label string, used, total int64, unit string, note string) string {
	if total <= 0 {
		return ""
	}
	frac := float64(used) / float64(total)
	pct := int(frac * 100)
	const w = 22
	gauge := styles.Gauge(frac, w, 0.75, 0.90)
	amounts := fmt.Sprintf("%s / %s", model.FmtBytes(used), model.FmtBytes(total))
	if unit == "segs" {
		amounts = fmt.Sprintf("%d / %d segs", used, total)
	}
	line := fmt.Sprintf(" %-17s %s %3d%%  %s", label, gauge, pct, amounts)
	if note != "" {
		line += styles.Dim().Render("  " + note)
	}
	return line + "\n"
}

// memoryPanel renders the Flink memory model with live usage — the Web UI's
// TaskManager boxes, greppable.
func (t *TMDetail) memoryPanel() (string, int) {
	mc := t.tm.MemoryConfiguration
	if mc.TotalProcessMemory == 0 {
		return "", 0
	}
	var b strings.Builder
	lines := 0
	b.WriteString(styles.Header().Render(" memory model") +
		styles.Dim().Render(fmt.Sprintf("  process total %s · flink %s", model.FmtBytes(mc.TotalProcessMemory), model.FmtBytes(mc.TotalFlinkMemory))) + "\n")
	lines++
	m := t.tm.Metrics
	rows := []string{
		memRow("JVM Heap", m.HeapUsed, m.HeapMax, "", "framework+task heap"),
		memRow("Managed", t.live.ManagedUsed, t.live.ManagedTotal, "", "state backend / batch ops"),
		memRow("Network buffers", t.live.NetworkTotal-t.live.NetworkAvail, t.live.NetworkTotal, "segs", "saturation here predicts backpressure"),
		memRow("Metaspace", t.live.MetaspaceUsed, t.live.MetaspaceMax, "", ""),
	}
	for _, r := range rows {
		if r != "" {
			b.WriteString(r)
			lines++
		}
	}
	b.WriteString(styles.Dim().Render(fmt.Sprintf(" off-heap: framework %s · task %s · jvm overhead %s",
		model.FmtBytes(mc.FrameworkOffHeap), model.FmtBytes(mc.TaskOffHeap), model.FmtBytes(mc.JVMOverhead))) + "\n")
	lines++
	if len(t.tm.AllocatedSlots) > 0 {
		byJob := map[string]int{}
		var order []string
		for _, sl := range t.tm.AllocatedSlots {
			if byJob[sl.JobID] == 0 {
				order = append(order, sl.JobID)
			}
			byJob[sl.JobID]++
		}
		parts := make([]string, 0, len(order))
		for _, jid := range order {
			name := t.jobs[jid]
			if name == "" {
				name = jid[:min(8, len(jid))]
			} else if len(name) > 24 {
				name = name[:23] + "…"
			}
			parts = append(parts, fmt.Sprintf("%s ×%d", name, byJob[jid]))
		}
		b.WriteString(" " + styles.OKText().Render("slots") + styles.Dim().Render(fmt.Sprintf(" %d/%d occupied by: ", t.tm.SlotsNumber-t.tm.FreeSlots, t.tm.SlotsNumber)) + strings.Join(parts, styles.Dim().Render(" · ")) + "\n")
		lines++
	}
	return b.String(), lines
}

func (t *TMDetail) View(width, height int) string {
	return framed(width, height, "TaskManager "+t.id, -1, "", t.viewBody)
}

func (t *TMDetail) viewBody(width, height int) string {
	m := t.tm.Metrics
	head := " " + heapLine(m.HeapUsed, m.HeapMax) +
		styles.Dim().Render(fmt.Sprintf("  ·  non-heap %s  ·  slots %d/%d  ·  %s",
			model.FmtBytes(m.NonHeapUsed), t.tm.FreeSlots, t.tm.SlotsNumber, t.tm.Path)) + "\n"
	used := 1
	if t.err != "" {
		head += styles.ErrText().Render("⚠ "+t.err) + "\n"
		used++
	}
	gc := " "
	for _, g := range m.GarbageCollectors {
		gc += fmt.Sprintf("gc:%s %d runs %s  ", g.Name, g.Count, model.FmtDuration(g.Time))
	}
	head += styles.Dim().Render(gc) + "\n"
	used++

	if !t.showLog {
		panel, _ := t.memoryPanel()
		return head + "\n" + panel + "\n" + styles.Dim().Render(" <l> log tail · <T> thread dump · <P> flame graph")
	}
	if !t.ready {
		t.vp = viewport.New(width, height-used)
		t.ready = true
		if t.logText != "" {
			t.vp.SetContent(t.logText)
			t.vp.GotoBottom()
		}
	}
	t.vp.Width = width
	t.vp.Height = height - used
	if t.logText == "" {
		t.vp.SetContent(styles.Dim().Render("loading log…"))
	}
	return head + t.vp.View()
}
