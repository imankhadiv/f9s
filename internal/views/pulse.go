package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

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
func gauge(used, total int, width int) string {
	if total <= 0 {
		return styles.Dim().Render(strings.Repeat("░", width))
	}
	pct := float64(used) / float64(total)
	n := int(pct * float64(width))
	st := styles.OKText()
	if pct >= 0.9 {
		st = styles.ErrText()
	} else if pct >= 0.75 {
		st = styles.WarnText()
	}
	return st.Render(strings.Repeat("█", n)) + styles.Dim().Render(strings.Repeat("░", width-n))
}

func (p *Pulse) View(width, height int) string {
	head := styles.ViewTitle().Render("Cluster pulse · " + p.deps.Ctx)
	if !p.loaded {
		if p.d.err != nil {
			return head + "\n" + styles.ErrText().Render(" ⚠ "+p.d.err.Error())
		}
		return head + "\n" + styles.Dim().Render(" reading the cluster…")
	}
	d := p.d
	var b strings.Builder
	b.WriteString(head + styles.Dim().Render(fmt.Sprintf("  flink %s", d.ov.FlinkVersion)) + "\n\n")

	// slot utilization gauge
	used := d.ov.SlotsTotal - d.ov.SlotsAvailable
	b.WriteString(fmt.Sprintf(" %-14s %s %d/%d\n", "slots", gauge(used, d.ov.SlotsTotal, 30), used, d.ov.SlotsTotal))
	b.WriteString(fmt.Sprintf(" %-14s %s\n", "", styles.Dim().Render(spark(p.slotH, floatMax(p.slotH), 30))))

	// job-state mix
	states := make([]string, 0, len(d.jobStates))
	for s := range d.jobStates {
		states = append(states, s)
	}
	sort.Strings(states)
	var parts []string
	for _, s := range states {
		st := styles.StateStyle(s)
		parts = append(parts, st.Render(fmt.Sprintf("%d %s", d.jobStates[s], s)))
	}
	b.WriteString(fmt.Sprintf(" %-14s %s · %d TMs\n\n", "jobs", strings.Join(parts, "  "), d.ov.TaskManagers))

	// throughput
	b.WriteString(fmt.Sprintf(" %-14s %s rec/s in\n", "throughput", styles.OKText().Render(fmtRate(d.totalIn))))
	b.WriteString(fmt.Sprintf(" %-14s %s\n\n", "", styles.Dim().Render(spark(p.inHist, floatMax(p.inHist), 30))))

	// backpressure verdict
	bpTxt, bpStyle := "clear", styles.OKText()
	switch d.worstBP {
	case "high":
		bpTxt, bpStyle = "HIGH on "+shortName(d.worstBPJob), styles.ErrText()
	case "low":
		bpTxt, bpStyle = "low on "+shortName(d.worstBPJob), styles.WarnText()
	}
	b.WriteString(fmt.Sprintf(" %-14s %s\n", "backpressure", bpStyle.Render(bpTxt)))

	// checkpoints
	ckStyle := styles.OKText()
	if d.ckptFail > 0 {
		ckStyle = styles.WarnText()
	}
	b.WriteString(fmt.Sprintf(" %-14s %s\n", "checkpoints", ckStyle.Render(fmt.Sprintf("%d completed · %d failed", d.ckptOK, d.ckptFail))))

	// alerts
	open := 0
	if p.deps.Sentinel != nil {
		open = len(p.deps.Sentinel.OpenAlerts())
	}
	alStyle := styles.OKText()
	alTxt := "none firing"
	if open > 0 {
		alStyle, alTxt = styles.ErrText(), fmt.Sprintf("%d FIRING — press ! ", open)
	}
	b.WriteString(fmt.Sprintf(" %-14s %s\n", "alerts", alStyle.Render(alTxt)))

	// overall verdict banner
	b.WriteString("\n")
	if open > 0 || d.worstBP == "high" || d.jobStates["FAILED"] > 0 || d.jobStates["RESTARTING"] > 0 {
		b.WriteString(" " + styles.ErrText().Render("● NEEDS ATTENTION") + styles.Dim().Render(" — drill in with :jobs or !"))
	} else if d.worstBP == "low" || d.ckptFail > 0 {
		b.WriteString(" " + styles.WarnText().Render("● WATCH") + styles.Dim().Render(" — soft signals present"))
	} else {
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
