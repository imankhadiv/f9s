package views

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/metricdict"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/pins"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/sahilm/fuzzy"
)

// metric scopes cycled with tab.
const (
	scopeJM = iota
	scopeTM
	scopeJob
	scopeVtx
	scopeCount
)

var scopeNames = [scopeCount]string{"JM", "TM", "JOB", "VTX"}

// metricItem is one searchable catalog entry.
type metricItem struct {
	display string // what fuzzy matches against and the list shows
	path    string // metrics endpoint
	keys    []string
	vid     string // set for vertex scope (per-subtask skew available)
	jid     string
	par     int
}

type catalogMsg struct {
	scope int
	items []metricItem
	err   error
}

type metricValueMsg struct {
	display string
	value   float64
	agg     string
	skew    int       // -1 n/a
	vals    []float64 // per-subtask values (vertex scope)
	err     error
}

type pinValueMsg struct {
	idx   int
	value float64
	err   error
}

// MetricsExplorer is the fuzzy metric palette + live inspector: every metric
// the cluster exposes, searchable, documented, and watchable.
type MetricsExplorer struct {
	deps      Deps
	job       model.JobOverview
	input     textinput.Model
	scope     int
	catalogs  [scopeCount][]metricItem
	matches   []fuzzy.Match
	cursor    int
	inspect   *metricItem
	history   []float64
	lastValue float64
	lastAgg   string
	lastSkew  int
	lastVals  []float64
	showSubs  bool
	pinned    []metricItem
	pinVals   map[int]float64
	err       string
}

// pinsToItems / itemsToPins convert between the persisted form and the
// explorer's internal one.
func pinsToItems(ps []pins.Pin) []metricItem {
	out := make([]metricItem, len(ps))
	for i, p := range ps {
		out[i] = metricItem{display: p.Display, path: p.Path, keys: p.Keys, vid: p.VID, jid: p.JID, par: p.Par}
	}
	return out
}

func itemsToPins(items []metricItem) []pins.Pin {
	out := make([]pins.Pin, len(items))
	for i, it := range items {
		out[i] = pins.Pin{Display: it.display, Path: it.path, Keys: it.keys, VID: it.vid, JID: it.jid, Par: it.par}
	}
	return out
}

// NewMetrics opens the explorer with the selected job as the JOB/VTX scope.
func NewMetrics(deps Deps, job model.JobOverview) *MetricsExplorer {
	in := textinput.New()
	in.Prompt = "> "
	in.CharLimit = 80
	in.Focus()
	return &MetricsExplorer{deps: deps, job: job, input: in, lastSkew: -1,
		pinned: pinsToItems(pins.Load(deps.Ctx)), pinVals: map[int]float64{}}
}

func (m *MetricsExplorer) Title() string   { return "metrics" }
func (m *MetricsExplorer) Filtering() bool { return m.inspect == nil }
func (m *MetricsExplorer) Bindings() []key.Binding {
	if m.inspect != nil {
		return []key.Binding{
			binding("s", "s", "subtask bars"),
			binding("a", "a", "AI: explain this value"),
			binding("ctrl+p", "ctrl-p", "pin/unpin"),
			binding("esc", "esc", "back to search"),
		}
	}
	return []key.Binding{
		binding("enter", "enter", "inspect"),
		binding("tab", "tab", "scope"),
		binding("ctrl+p", "ctrl-p", "pin/unpin"),
		binding("esc", "esc", "back"),
	}
}

func (m *MetricsExplorer) Init() tea.Cmd {
	cl := m.deps.CL
	jid := m.job.JID
	cmds := []tea.Cmd{
		m.loadCatalog(scopeJM, "/jobmanager/metrics", nil),
		m.loadCatalog(scopeTM, "/taskmanagers/metrics", nil),
	}
	if jid != "" {
		cmds = append(cmds, m.loadCatalog(scopeJob, "/jobs/"+jid+"/metrics", nil))
		cmds = append(cmds, func() tea.Msg {
			job, err := cl.Job(jid)
			if err != nil {
				return catalogMsg{scope: scopeVtx, err: err}
			}
			var items []metricItem
			for _, v := range job.Vertices {
				names, err := cl.ListMetricNames("/jobs/" + jid + "/vertices/" + v.ID + "/subtasks/metrics")
				if err != nil {
					continue
				}
				short := v.Name
				if len(short) > 24 {
					short = short[:23] + "…"
				}
				for _, n := range names {
					items = append(items, metricItem{
						display: short + " / " + n,
						path:    "/jobs/" + jid + "/vertices/" + v.ID + "/subtasks/metrics",
						keys:    []string{n},
						vid:     v.ID,
						jid:     jid,
						par:     v.Parallelism,
					})
				}
			}
			return catalogMsg{scope: scopeVtx, items: items}
		})
	}
	return tea.Batch(cmds...)
}

func (m *MetricsExplorer) loadCatalog(scope int, path string, _ []string) tea.Cmd {
	cl := m.deps.CL
	return func() tea.Msg {
		names, err := cl.ListMetricNames(path)
		if err != nil {
			return catalogMsg{scope: scope, err: err}
		}
		items := make([]metricItem, len(names))
		for i, n := range names {
			items[i] = metricItem{display: n, path: path, keys: []string{n}}
		}
		return catalogMsg{scope: scope, items: items}
	}
}

// Refresh polls the inspected metric, or the pinned tiles in palette mode.
func (m *MetricsExplorer) Refresh() tea.Cmd {
	if m.inspect == nil {
		cl := m.deps.CL
		var cmds []tea.Cmd
		for i, it := range m.pinned {
			i, it := i, it
			cmds = append(cmds, func() tea.Msg {
				ms, err := cl.MetricValues(it.path, it.keys)
				if err != nil || len(ms) == 0 {
					return pinValueMsg{idx: i, err: fmt.Errorf("pin: %v", err)}
				}
				val, ok := ms[0].Float()
				if !ok {
					return pinValueMsg{idx: i, err: fmt.Errorf("pin: no value")}
				}
				return pinValueMsg{idx: i, value: val}
			})
		}
		if len(cmds) == 0 {
			return nil
		}
		return tea.Batch(cmds...)
	}
	cl := m.deps.CL
	it := *m.inspect
	return func() tea.Msg {
		ms, err := cl.MetricValues(it.path, it.keys)
		if err != nil || len(ms) == 0 {
			return metricValueMsg{display: it.display, err: fmt.Errorf("fetch: %v", err)}
		}
		val, _ := ms[0].Float()
		out := metricValueMsg{display: it.display, value: val, skew: -1}
		if ms[0].Value == "" && ms[0].Min != nil && ms[0].Max != nil && ms[0].Avg != nil {
			out.agg = fmt.Sprintf("min %.0f · avg %.0f · max %.0f (across the fleet)", *ms[0].Min, *ms[0].Avg, *ms[0].Max)
		}
		if it.vid != "" && it.par >= 2 {
			if vals, err := cl.SubtaskMetric(it.jid, it.vid, it.keys[0], it.par); err == nil && len(vals) > 0 {
				minV, maxV, sum := vals[0], vals[0], 0.0
				for _, v := range vals {
					if v < minV {
						minV = v
					}
					if v > maxV {
						maxV = v
					}
					sum += v
				}
				out.agg = fmt.Sprintf("min %.0f · avg %.0f · max %.0f", minV, sum/float64(len(vals)), maxV)
				out.skew = analyze.SkewScore(vals)
			}
		}
		return out
	}
}

// scopePrefixes maps a typed prefix to a scope: "tm:heap" jumps to the TM
// catalog and searches "heap".
var scopePrefixes = map[string]int{
	"jm": scopeJM, "tm": scopeTM, "job": scopeJob,
	"vtx": scopeVtx, "vertex": scopeVtx, "v": scopeVtx,
}

func (m *MetricsExplorer) search() {
	q := m.input.Value()
	if i := strings.IndexByte(q, ':'); i > 0 {
		if sc, ok := scopePrefixes[strings.ToLower(q[:i])]; ok {
			m.scope = sc
			q = q[i+1:]
		}
	}
	items := m.catalogs[m.scope]
	targets := make([]string, len(items))
	for i, it := range items {
		targets[i] = it.display
	}
	if q == "" {
		m.matches = m.matches[:0]
		for i := range targets {
			if i >= 200 {
				break
			}
			m.matches = append(m.matches, fuzzy.Match{Str: targets[i], Index: i})
		}
	} else {
		m.matches = fuzzy.Find(q, targets)
	}
	if m.cursor >= len(m.matches) {
		m.cursor = len(m.matches) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *MetricsExplorer) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case catalogMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.catalogs[msg.scope] = msg.items
		m.search()
		return m, nil

	case pinValueMsg:
		if msg.err == nil {
			m.pinVals[msg.idx] = msg.value
		}
		return m, nil

	case metricValueMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.lastValue = msg.value
		m.lastAgg = msg.agg
		m.lastSkew = msg.skew
		m.lastVals = msg.vals
		m.history = append(m.history, msg.value)
		if len(m.history) > 120 {
			m.history = m.history[len(m.history)-120:]
		}
		return m, nil

	case tea.KeyMsg:
		if m.inspect != nil {
			switch msg.String() {
			case "esc":
				m.inspect = nil
				m.history = nil
				m.lastVals = nil
				m.showSubs = false
				return m, nil
			case "s":
				m.showSubs = !m.showSubs
				return m, nil
			case "ctrl+p":
				return m, m.togglePin(*m.inspect)
			case "a":
				it := *m.inspect
				jid := it.jid
				if jid == "" {
					jid = m.job.JID
				}
				if jid == "" {
					return m, flash("no job in scope for AI analysis")
				}
				extra := fmt.Sprintf("\nMETRIC ANALYSIS metric=%q current-value=%.2f %s\nQUESTION: is this value healthy for this pipeline? If not, what should change?\n",
					it.display, m.lastValue, m.lastAgg)
				return m, explainJobWithExtra(m.deps, jid, m.job.Name, extra)
			}
			return m, nil
		}
		switch msg.String() {
		case "tab":
			m.scope = (m.scope + 1) % scopeCount
			m.cursor = 0
			m.search()
			return m, nil
		case "up":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down":
			if m.cursor < len(m.matches)-1 {
				m.cursor++
			}
			return m, nil
		case "enter":
			if m.cursor < len(m.matches) {
				it := m.catalogs[m.scope][m.matches[m.cursor].Index]
				m.inspect = &it
				m.history = nil
				return m, m.Refresh()
			}
			return m, nil
		case "ctrl+p":
			if m.cursor < len(m.matches) {
				return m, m.togglePin(m.catalogs[m.scope][m.matches[m.cursor].Index])
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.search()
		return m, cmd
	}
	return m, nil
}

// togglePin pins or unpins a metric and persists the set for the context.
func (m *MetricsExplorer) togglePin(it metricItem) tea.Cmd {
	for i, p := range m.pinned {
		if p.display == it.display && p.path == it.path {
			m.pinned = append(m.pinned[:i], m.pinned[i+1:]...)
			delete(m.pinVals, i)
			m.pinVals = map[int]float64{}
			pins.Save(m.deps.Ctx, itemsToPins(m.pinned))
			return flash("unpinned " + it.display)
		}
	}
	m.pinned = append(m.pinned, it)
	pins.Save(m.deps.Ctx, itemsToPins(m.pinned))
	return tea.Batch(flash("pinned "+it.display), m.Refresh())
}

// pinStrip renders the pinned tiles with their last-polled values.
func (m *MetricsExplorer) pinStrip() string {
	if len(m.pinned) == 0 {
		return ""
	}
	var parts []string
	for i, p := range m.pinned {
		val := "…"
		if v, ok := m.pinVals[i]; ok {
			val = strconv.FormatFloat(v, 'f', -1, 64)
			if len(val) > 12 {
				val = fmt.Sprintf("%.3g", v)
			}
		}
		name := p.display
		if len(name) > 28 {
			name = name[:27] + "…"
		}
		parts = append(parts, styles.Dim().Render(name+" ")+styles.OKText().Render(val))
	}
	return " ⚲ " + strings.Join(parts, styles.Dim().Render("  │  ")) + "\n"
}

func highlight(match fuzzy.Match) string {
	if len(match.MatchedIndexes) == 0 {
		return match.Str
	}
	set := map[int]bool{}
	for _, i := range match.MatchedIndexes {
		set[i] = true
	}
	var b strings.Builder
	for i, r := range match.Str {
		if set[i] {
			b.WriteString(styles.Header().Render(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (m *MetricsExplorer) View(width, height int) string {
	var b strings.Builder
	if m.inspect != nil {
		it := *m.inspect
		info, known := metricdict.Lookup(it.keys[0])
		b.WriteString(styles.ViewTitle().Render("Metric · "+it.display) + "\n")
		if known {
			b.WriteString(styles.Dim().Render(fmt.Sprintf(" %s · %s · %s", info.Category, info.Unit, info.Desc)) + "\n")
		}
		b.WriteString(fmt.Sprintf("\n now: %s\n", styles.OKText().Render(fmt.Sprintf("%.2f", m.lastValue))))
		if m.lastAgg != "" {
			skewTxt := ""
			if m.lastSkew >= 0 {
				st := styles.OKText()
				if m.lastSkew >= 30 {
					st = styles.WarnText()
				}
				if m.lastSkew > 60 {
					st = styles.ErrText()
				}
				skewTxt = " · " + st.Render(fmt.Sprintf("skew %d%%", m.lastSkew))
			}
			b.WriteString(" " + styles.Dim().Render(m.lastAgg) + skewTxt + "\n")
		}
		if m.showSubs && len(m.lastVals) >= 2 {
			b.WriteString("\n")
			type sr struct {
				idx int
				v   float64
			}
			rows := make([]sr, len(m.lastVals))
			for i, v := range m.lastVals {
				rows[i] = sr{i, v}
			}
			sort.Slice(rows, func(a, b int) bool { return rows[a].v > rows[b].v })
			maxV := rows[0].v
			barW := width - 30
			if barW < 10 {
				barW = 10
			}
			for _, r := range rows {
				b.WriteString(fmt.Sprintf(" #%-3d %s %10.1f\n", r.idx,
					styles.Dim().Render(fmt.Sprintf("%-*s", barW, bar(r.v, maxV, barW))), r.v))
			}
		} else if len(m.lastVals) >= 2 {
			b.WriteString(styles.Dim().Render("\n <s> shows the per-subtask bars") + "\n")
		}
		if len(m.history) > 1 {
			maxV := 0.0
			for _, v := range m.history {
				if v > maxV {
					maxV = v
				}
			}
			b.WriteString("\n " + styles.OKText().Render(spark(m.history, maxV, width-4)) + "\n")
			b.WriteString(styles.Dim().Render(fmt.Sprintf(" %d samples · polled every refresh tick", len(m.history))) + "\n")
		}
		if m.err != "" {
			b.WriteString(styles.ErrText().Render(" ⚠ "+m.err) + "\n")
		}
		return b.String()
	}

	tabs := make([]string, scopeCount)
	for i, n := range scopeNames {
		if i == m.scope {
			tabs[i] = styles.Header().Render("[" + n + "]")
		} else {
			tabs[i] = styles.Dim().Render(" " + n + " ")
		}
	}
	b.WriteString(m.pinStrip())
	b.WriteString(" " + m.input.View() +
		styles.Dim().Render(fmt.Sprintf("   %d/%d  ", len(m.matches), len(m.catalogs[m.scope]))) +
		strings.Join(tabs, "") + "\n")
	if m.err != "" {
		b.WriteString(styles.ErrText().Render(" ⚠ "+m.err) + "\n")
	}

	visible := height - 4
	start := 0
	if m.cursor >= visible {
		start = m.cursor - visible + 1
	}
	for i := start; i < len(m.matches) && i < start+visible; i++ {
		match := m.matches[i]
		prefix := "  "
		if i == m.cursor {
			prefix = styles.Header().Render("❯ ")
		}
		line := highlight(match)
		if info, ok := metricdict.Lookup(m.catalogs[m.scope][match.Index].keys[0]); ok {
			line += styles.Dim().Render("  " + info.Category + " · " + info.Unit + " · " + info.Desc)
		}
		b.WriteString(prefix + line + "\n")
	}
	if len(m.matches) == 0 {
		b.WriteString(styles.Dim().Render("  no metrics match — try another scope (tab)"))
	}
	// footer metadata for the cursor row
	if m.cursor < len(m.matches) {
		id := m.catalogs[m.scope][m.matches[m.cursor].Index].keys[0]
		if info, ok := metricdict.Lookup(id); ok {
			b.WriteString("\n" + styles.Dim().Render(" ▸ "+id+" — "+info.Desc+" ("+info.Unit+")"))
		}
	}
	return b.String()
}
