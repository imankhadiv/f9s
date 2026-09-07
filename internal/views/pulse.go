package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type pulseData struct {
	ov         model.ClusterOverview
	jobStates  map[string]int // RUNNING/FAILED/... → count
	totalIn    float64        // records/s in across all running sources
	worstBP    string         // worst backpressure level seen
	worstBPJob string
	ckptOK     int
	ckptFail   int
	err        error
}

type pulseMsg struct{ d pulseData }

// Pulse is the cluster heartbeat: one screen answering "is everything OK?"
// — slot utilization, job-state mix, aggregate throughput, alert count,
// checkpoint health, and a throughput sparkline. k9s's pulse, for Flink.
type Pulse struct {
	deps   Deps
	d      pulseData
	inHist []float64
	slotH  []float64
	loaded bool
}

// NewPulse builds the dashboard.
func NewPulse(deps Deps) *Pulse { return &Pulse{deps: deps} }

func (p *Pulse) Title() string   { return "pulse" }
func (p *Pulse) Filtering() bool { return false }
func (p *Pulse) Bindings() []key.Binding {
	return []key.Binding{binding("esc", "esc", "back")}
}

func (p *Pulse) Init() tea.Cmd { return p.Refresh() }

func (p *Pulse) Refresh() tea.Cmd {
	cl := p.deps.CL
	return func() tea.Msg {
		d := pulseData{jobStates: map[string]int{}}
		ov, err := cl.Overview()
		if err != nil {
			return pulseMsg{d: pulseData{err: err}}
		}
		d.ov = ov
		jobs, err := cl.Jobs()
		if err != nil {
			return pulseMsg{d: pulseData{err: err}}
		}
		bpRank := map[string]int{"": 0, "ok": 1, "low": 2, "high": 3}
		for _, j := range jobs {
			d.jobStates[j.State]++
			if j.State != "RUNNING" {
				continue
			}
			job, err := cl.Job(j.JID)
			if err != nil {
				continue
			}
			for _, v := range job.Vertices {
				if strings.HasPrefix(v.Name, "Source") {
					if r, _, e := cl.VertexOutRates(j.JID, v.ID); e == nil && r > 0 {
						d.totalIn += r
					}
				}
				if bp, e := cl.Backpressure(j.JID, v.ID); e == nil {
					if bpRank[bp.Level()] > bpRank[d.worstBP] {
						d.worstBP, d.worstBPJob = bp.Level(), j.Name
					}
				}
			}
			if cp, e := cl.Checkpoints(j.JID); e == nil {
				d.ckptOK += cp.Counts.Completed
				d.ckptFail += cp.Counts.Failed
			}
		}
		return pulseMsg{d: d}
	}
}

func (p *Pulse) Update(msg tea.Msg) (Model, tea.Cmd) {
	if m, ok := msg.(pulseMsg); ok {
		if m.d.err == nil {
			p.d = m.d
			p.loaded = true
			p.inHist = pushCap(p.inHist, m.d.totalIn, 60)
			used := m.d.ov.SlotsTotal - m.d.ov.SlotsAvailable
			p.slotH = pushCap(p.slotH, float64(used), 60)
		} else {
			p.d.err = m.d.err
		}
	}
	return p, nil
}

func pushCap(s []float64, v float64, n int) []float64 {
	s = append(s, v)
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}

// gauge renders a proportional bar with severity coloring.
// gauge delegates to the shared widget; an unknown total renders empty.
func gauge(used, total int, width int) string {
	if total <= 0 {
		return styles.Dim().Render(strings.Repeat("░", width))
	}
	return styles.Gauge(float64(used)/float64(total), width, 0.75, 0.90)
}

// AIContext summarizes the on-screen cluster pulse for the AI panel.
func (p *Pulse) AIContext() (string, []string) {
	var b strings.Builder
	b.WriteString("## On screen: cluster pulse\n")
	if !p.loaded {
		b.WriteString("still loading cluster state\n")
		return b.String(), nil
	}
	d := p.d
	open := 0
	if p.deps.Sentinel != nil {
		open = len(p.deps.Sentinel.OpenAlerts())
	}
	verdict := "HEALTHY"
	switch {
	case open > 0 || d.worstBP == "high" || d.jobStates["FAILED"] > 0 || d.jobStates["RESTARTING"] > 0:
		verdict = "NEEDS ATTENTION"
	case d.worstBP == "low" || d.ckptFail > 0:
		verdict = "WATCH"
	}
	fmt.Fprintf(&b, "verdict: %s\n", verdict)
	usedSlots := d.ov.SlotsTotal - d.ov.SlotsAvailable
	fmt.Fprintf(&b, "slots: %d/%d used · %d TaskManagers\n", usedSlots, d.ov.SlotsTotal, d.ov.TaskManagers)
	states := make([]string, 0, len(d.jobStates))
	for s := range d.jobStates {
		states = append(states, s)
	}
	sort.Strings(states)
	parts := make([]string, 0, len(states))
	for _, s := range states {
		parts = append(parts, fmt.Sprintf("%d %s", d.jobStates[s], s))
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, "jobs: %s\n", strings.Join(parts, " · "))
	}
	bp := d.worstBP
	if bp == "" {
		bp = "clear"
	}
	if d.worstBPJob != "" {
		fmt.Fprintf(&b, "worst backpressure: %s (%s)\n", bp, d.worstBPJob)
	} else {
		fmt.Fprintf(&b, "worst backpressure: %s\n", bp)
	}
	fmt.Fprintf(&b, "checkpoints: %d completed · %d failed\n", d.ckptOK, d.ckptFail)
	fmt.Fprintf(&b, "throughput: %s rec/s in\n", fmtRate(d.totalIn))
	if open > 0 {
		fmt.Fprintf(&b, "alerts: %d firing\n", open)
	}
	var names []string
	if d.worstBPJob != "" {
		names = []string{d.worstBPJob}
	}
	return b.String(), names
}

func (p *Pulse) View(width, height int) string {
	head := styles.ViewTitle().Render("Cluster pulse · " + p.deps.Ctx)
	if !p.loaded {
		msg := styles.Dim().Render("reading the cluster…")
		if p.d.err != nil {
			msg = styles.ErrText().Render("⚠ " + p.d.err.Error())
		}
		return head + "\n" + centered(width, height-2, msg)
	}
	d := p.d
	open := 0
	if p.deps.Sentinel != nil {
		open = len(p.deps.Sentinel.OpenAlerts())
	}

	// tile bodies -----------------------------------------------------------
	cols := 3
	tileW := (width - 2) / cols
	if tileW < 26 {
		cols, tileW = 1, width-1
	}
	iw := tileW - 4 // inner text width (border + padding)
	if iw < 6 {
		iw = 6
	}
	gw := iw
	if gw > 24 {
		gw = 24
	}

	used := d.ov.SlotsTotal - d.ov.SlotsAvailable
	slotsBody := gauge(used, d.ov.SlotsTotal, gw) + "\n" +
		fmt.Sprintf("%d/%d slots", used, d.ov.SlotsTotal) + "\n" +
		styles.Dim().Render(spark(p.slotH, floatMax(p.slotH), gw))

	states := make([]string, 0, len(d.jobStates))
	for s := range d.jobStates {
		states = append(states, s)
	}
	sort.Strings(states)
	jl := make([]string, 0, len(states)+1)
	for _, s := range states {
		jl = append(jl, styles.StateStyle(s).Render(fmt.Sprintf("%d %s", d.jobStates[s], s)))
	}
	jl = append(jl, styles.Dim().Render(fmt.Sprintf("%d TMs", d.ov.TaskManagers)))
	jobsBody := strings.Join(jl, "\n")

	tpBody := styles.OKText().Render(fmtRate(d.totalIn)) + styles.Dim().Render(" rec/s in") + "\n" +
		styles.Dim().Render(spark(p.inHist, floatMax(p.inHist), gw))

	bpTxt, bpStyle := "clear", styles.OKText()
	switch d.worstBP {
	case "high":
		bpTxt, bpStyle = "HIGH", styles.ErrText()
	case "low":
		bpTxt, bpStyle = "low", styles.WarnText()
	}
	bpBody := bpStyle.Render(bpTxt)
	if d.worstBP != "" && d.worstBPJob != "" {
		bpBody += "\n" + styles.Dim().Render(shortName(d.worstBPJob))
	}

	ckStyle := styles.OKText()
	if d.ckptFail > 0 {
		ckStyle = styles.WarnText()
	}
	ckBody := ckStyle.Render(fmt.Sprintf("%d completed", d.ckptOK)) + "\n" +
		ckStyle.Render(fmt.Sprintf("%d failed", d.ckptFail))

	alStyle, alTxt := styles.OKText(), "none firing"
	if open > 0 {
		alStyle, alTxt = styles.ErrText(), fmt.Sprintf("%d FIRING", open)
	}
	alBody := alStyle.Render(alTxt) + "\n" + styles.Dim().Render("press ! for alerts")

	tiles := []struct{ title, body string }{
		{"SLOTS", slotsBody},
		{"JOBS", jobsBody},
		{"THROUGHPUT", tpBody},
		{"BACKPRESSURE", bpBody},
		{"CHECKPOINTS", ckBody},
		{"ALERTS", alBody},
	}

	// layout: grid of tiles, or a single stacked column on a narrow terminal
	var b strings.Builder
	b.WriteString(head + styles.Dim().Render(fmt.Sprintf("  flink %s", d.ov.FlinkVersion)) + "\n\n")
	if tileW >= 4 {
		var rows []string
		for i := 0; i < len(tiles); i += cols {
			var rt []string
			for j := i; j < i+cols && j < len(tiles); j++ {
				rt = append(rt, styles.Panel(tiles[j].title, tiles[j].body, tileW))
			}
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, rt...))
		}
		b.WriteString(strings.Join(rows, "\n"))
	}

	// overall verdict banner
	b.WriteString("\n\n")
	switch {
	case open > 0 || d.worstBP == "high" || d.jobStates["FAILED"] > 0 || d.jobStates["RESTARTING"] > 0:
		b.WriteString(" " + styles.ErrText().Render("● NEEDS ATTENTION") + styles.Dim().Render(" — drill in with :jobs or !"))
	case d.worstBP == "low" || d.ckptFail > 0:
		b.WriteString(" " + styles.WarnText().Render("● WATCH") + styles.Dim().Render(" — soft signals present"))
	default:
		b.WriteString(" " + styles.OKText().Render("● HEALTHY") + styles.Dim().Render(" — all clear"))
	}
	b.WriteString(styles.Dim().Render(fmt.Sprintf("   %s", time.Now().Format("15:04:05"))))
	return b.String()
}

func floatMax(s []float64) float64 {
	m := 0.0
	for _, v := range s {
		if v > m {
			m = v
		}
	}
	return m
}

func shortName(s string) string {
	if len(s) > 30 {
		return s[:29] + "…"
	}
	return s
}
