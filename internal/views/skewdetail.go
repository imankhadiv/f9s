package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/sentinel"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type skewDetailMsg struct {
	recs []float64
	busy []float64
	err  error
}

// SkewDetail is the per-subtask distribution panel for one vertex: sorted
// bars with the hot subtasks as labeled rows, the FLIP-418 score, and the
// cause classifier's verdict with its cause-mapped fix.
type SkewDetail struct {
	deps     Deps
	jid      string
	vid      string
	vertex   string
	par      int
	isSource bool
	recs     []float64
	busy     []float64
	err      string
}

// NewSkewDetail builds the distribution panel.
func NewSkewDetail(deps Deps, jid, vid, vertex string, par int, isSource bool) *SkewDetail {
	return &SkewDetail{deps: deps, jid: jid, vid: vid, vertex: vertex, par: par, isSource: isSource}
}

func (s *SkewDetail) Title() string   { return "skew" }
func (s *SkewDetail) Filtering() bool { return false }
func (s *SkewDetail) Bindings() []key.Binding {
	return []key.Binding{binding("f", "f", "AI: suggest fix"), binding("esc", "esc", "back")}
}

func (s *SkewDetail) Init() tea.Cmd { return s.Refresh() }

func (s *SkewDetail) Refresh() tea.Cmd {
	cl := s.deps.CL
	jid, vid, par := s.jid, s.vid, s.par
	return func() tea.Msg {
		recs, err := cl.VertexSubtaskRecords(jid, vid, par)
		if err != nil {
			return skewDetailMsg{err: err}
		}
		busy, _ := cl.VertexSubtaskBusy(jid, vid, par)
		return skewDetailMsg{recs: recs, busy: busy}
	}
}

func (s *SkewDetail) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "f" && len(s.recs) >= 2 {
			d := analyze.ClassifySkew(s.isSource, s.recs, s.busy)
			extra := fmt.Sprintf("\nSKEW ANALYSIS vertex=%q score=%d%% max/avg=%.1fx per-subtask-records=%v classified-cause=%q suggested-fix=%q\nQUESTION: given this skew, propose the exact code or config change for THIS pipeline.\n",
				s.vertex, analyze.SkewScore(s.recs), analyze.MaxAvgRatio(s.recs), s.recs, d.Cause, d.Fix)
			return s, explainJobWithExtra(s.deps, s.jid, s.vertex, extra)
		}
		return s, nil
	case skewDetailMsg:
		if msg.err != nil {
			s.err = msg.err.Error()
			return s, nil
		}
		s.err = ""
		s.recs, s.busy = msg.recs, msg.busy
	}
	return s, nil
}

func bar(v, max float64, width int) string {
	if max <= 0 {
		return ""
	}
	n := int(v / max * float64(width))
	if n > width {
		n = width
	}
	return strings.Repeat("█", n)
}

func (s *SkewDetail) View(width, height int) string {
	var b strings.Builder
	score := analyze.SkewScore(s.recs)
	ratio := analyze.MaxAvgRatio(s.recs)
	head := styles.ViewTitle().Render("Subtask distribution · "+s.vertex) + "\n"
	b.WriteString(head)
	if s.err != "" {
		return b.String() + styles.ErrText().Render(" ⚠ "+s.err)
	}
	if len(s.recs) < 2 {
		return b.String() + styles.Dim().Render(" parallelism 1 — skew does not apply")
	}
	scoreStyle := styles.OKText()
	if score >= 30 {
		scoreStyle = styles.WarnText()
	}
	if score > 60 {
		scoreStyle = styles.ErrText()
	}
	trend := ""
	if s.deps.Sentinel != nil {
		switch s.deps.Sentinel.SkewTrend(sentinel.Key{JID: s.jid, VID: s.vid}) {
		case "rising":
			trend = "  " + styles.ErrText().Render("↗ rising")
		case "falling":
			trend = "  " + styles.OKText().Render("↘ falling")
		case "stable":
			trend = "  " + styles.Dim().Render("→ stable")
		}
	}
	computeSkew := ""
	if len(s.busy) >= 2 {
		computeSkew = styles.Dim().Render(fmt.Sprintf("  ·  compute skew %d%%", analyze.SkewScore(s.busy)))
	}
	b.WriteString(" " + scoreStyle.Render(fmt.Sprintf("skew %d%%", score)) + trend + computeSkew +
		styles.Dim().Render(fmt.Sprintf("  ·  hottest subtask does %.1f× its fair share  ·  records/s per subtask", ratio)) + "\n\n")

	// diagnosis first — the verdict is the point
	if d := analyze.ClassifySkew(s.isSource, s.recs, s.busy); d.Cause != analyze.CauseNone {
		b.WriteString(" " + styles.WarnText().Render("cause: "+string(d.Cause)) + styles.Dim().Render(" — "+d.Detail) + "\n")
		b.WriteString(" " + styles.OKText().Render("fix: ") + d.Fix + "\n\n")
	}

	// sorted per-subtask bars, hot rows labeled with their real index
	type row struct {
		idx  int
		rate float64
		busy float64
	}
	rows := make([]row, len(s.recs))
	for i := range s.recs {
		bz := -1.0
		if i < len(s.busy) {
			bz = s.busy[i]
		}
		rows[i] = row{idx: i, rate: s.recs[i], busy: bz}
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].rate > rows[b].rate })
	maxRate := rows[0].rate
	barW := width - 46
	if barW < 12 {
		barW = 12
	}
	shown := rows
	if len(shown) > height-8 && height > 10 {
		shown = shown[:height-8] // hottest first; never bin away the max
	}
	for i, r := range shown {
		marker := "  "
		style := styles.Dim()
		if i == 0 && score >= 30 {
			marker = "▶ "
			style = styles.ErrText()
		}
		busyTxt := "-"
		if r.busy >= 0 {
			busyTxt = fmt.Sprintf("%d%%", int(r.busy/10))
		}
		b.WriteString(fmt.Sprintf(" %s#%-3d %s %s  %s busy %s\n",
			marker, r.idx, style.Render(fmt.Sprintf("%-*s", barW, bar(r.rate, maxRate, barW))),
			fmt.Sprintf("%8.0f/s", r.rate), styles.Dim().Render("·"), busyTxt))
	}
	if len(shown) < len(rows) {
		b.WriteString(styles.Dim().Render(fmt.Sprintf(" … %d cooler subtasks not shown\n", len(rows)-len(shown))))
	}
	return b.String()
}
