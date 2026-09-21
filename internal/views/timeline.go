package views

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aymanbagabas/go-osc52/v2"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/kube"
	"github.com/imankhadiv/f9s/internal/ledger"
	"github.com/imankhadiv/f9s/internal/snapshot"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

// timelineEvent is one entry of the merged incident feed.
type timelineEvent struct {
	at    time.Time
	glyph string
	text  string
	sty   lipgloss.Style
}

// Timeline is the incident feed: alert fires/clears, savepoint/stop
// actions, and AI investigations merged chronologically — the 3am causal
// chain in one scrollable view instead of four separate lookups.
type Timeline struct {
	deps  Deps
	vp    viewport.Model
	ready bool

	scaling     []analyze.ScalingEvent // operator autoscaler decisions (via kubectl)
	scaleAt     time.Time
	loadScaling func(kubeContext string) ([]kube.Event, error)
}

type timelineScalingMsg struct{ evs []analyze.ScalingEvent }

// NewTimeline builds the merged feed view.
func NewTimeline(deps Deps) *Timeline {
	return &Timeline{deps: deps, loadScaling: func(kc string) ([]kube.Event, error) {
		return kube.EventsByReason(kc, "ScalingReport")
	}}
}

func (t *Timeline) Title() string   { return "timeline" }
func (t *Timeline) Filtering() bool { return false }
func (t *Timeline) Bindings() []key.Binding {
	return []key.Binding{
		binding("up", "↑↓/pgup", "scroll"),
		binding("E", "E", "export postmortem"),
		binding("esc", "esc", "back"),
	}
}

func (t *Timeline) Init() tea.Cmd { return t.scalingCmd() }

// Refresh only re-reads the operator's scaling events (throttled); everything
// else is built from in-memory + disk state each frame.
func (t *Timeline) Refresh() tea.Cmd { return t.scalingCmd() }

// scalingCmd fetches every ScalingReport on the cluster in one kubectl call
// (when the context maps a kubeContext), at most every 30s.
func (t *Timeline) scalingCmd() tea.Cmd {
	kc, err := configuredKubeContext(t.deps)
	if err != nil || time.Since(t.scaleAt) < autoRefreshEvery {
		return nil
	}
	t.scaleAt = time.Now()
	load := t.loadScaling
	return func() tea.Msg {
		es, err := load(kc)
		if err != nil {
			return nil
		}
		return timelineScalingMsg{evs: scalingEvents(es)}
	}
}

// scalingEvents turns ScalingReport events into per-resource decisions.
func scalingEvents(es []kube.Event) []analyze.ScalingEvent {
	var out []analyze.ScalingEvent
	for _, e := range es {
		cs := analyze.ParseScalingReport(e.Message)
		if len(cs) == 0 {
			continue
		}
		out = append(out, analyze.ScalingEvent{At: e.Last, Resource: e.Namespace + "/" + e.Name, Changes: cs})
	}
	return out
}

// scalingTimeline renders autoscaler decisions and flapping verdicts.
func scalingTimeline(evs []analyze.ScalingEvent) []timelineEvent {
	var out []timelineEvent
	for _, e := range evs {
		parts := make([]string, len(e.Changes))
		applied := true
		for i, c := range e.Changes {
			id := c.VID
			if len(id) > 8 {
				id = id[:8]
			}
			parts[i] = fmt.Sprintf("%s %d→%d", id, c.From, c.To)
			applied = applied && c.Applied
		}
		how := "applied"
		if !applied {
			how = "recommended only"
		}
		out = append(out, timelineEvent{e.At, "⤢", fmt.Sprintf("autoscaler · %s · %s (%s)", e.Resource, strings.Join(parts, ", "), how), styles.StateText()})
	}
	for _, f := range analyze.Flapping(evs, time.Hour) {
		out = append(out, timelineEvent{f.At, "⇅", "AUTOSCALER FLAPPING: " + f.String() +
			" within 1h — widen job.autoscaler.target.utilization.boundary or lengthen scale-down.interval", styles.ErrText()})
	}
	return out
}

// snapshotJournal converts the per-job config journal into change events
// (skipping first-sightings — only actual changes tell an incident story).
func snapshotJournal(ctx string) []timelineEvent {
	var out []timelineEvent
	byJob := map[string][]snapshot.Snap{}
	for _, s := range snapshot.Load(ctx, "") {
		byJob[s.JID] = append(byJob[s.JID], s)
	}
	for _, snaps := range byJob {
		for i := 1; i < len(snaps); i++ {
			diffs := snapshot.Diff(snaps[i-1], snaps[i])
			if len(diffs) == 0 {
				continue
			}
			out = append(out, timelineEvent{snaps[i].Time, "⚙",
				fmt.Sprintf("config changed · %s: %s", snaps[i].Name, strings.Join(diffs, "; ")), styles.WarnText()})
		}
	}
	return out
}

// collect merges every event source, oldest first.
func (t *Timeline) collect() []timelineEvent {
	var evs []timelineEvent
	if s := t.deps.Sentinel; s != nil {
		for _, al := range s.Episodes() {
			evs = append(evs,
				timelineEvent{al.OpenedAt, "⚠", fmt.Sprintf("%s alert FIRED · %s / %s (peak %s)", al.Kind, al.Job, al.Vertex, al.Peak), styles.ErrText()},
				timelineEvent{al.ClosedAt, "✓", fmt.Sprintf("%s alert cleared · %s / %s after %s", al.Kind, al.Job, al.Vertex, al.ClosedAt.Sub(al.OpenedAt).Round(time.Second)), styles.OKText()})
		}
		for _, al := range s.OpenAlerts() {
			evs = append(evs, timelineEvent{al.OpenedAt, "⚠", fmt.Sprintf("%s alert FIRED · %s / %s (peak %s) — STILL FIRING", al.Kind, al.Job, al.Vertex, al.Peak), styles.ErrText()})
		}
	}
	for _, sn := range snapshotJournal(t.deps.Ctx) {
		evs = append(evs, sn)
	}
	if entries, err := ledger.List(t.deps.Ctx); err == nil {
		for _, e := range entries {
			evs = append(evs, timelineEvent{e.Time, "⬢", fmt.Sprintf("%s · %s → %s", e.Action, e.Job, e.Path), styles.WarnText()})
		}
	}
	if w := t.deps.Watch; w != nil {
		for _, r := range w.List() {
			line := fmt.Sprintf("AI investigation (%s) · %s", r.Trigger, r.Job)
			if r.Err != "" {
				line += " — failed: " + r.Err
			} else if r.Answer != "" {
				first := strings.SplitN(strings.TrimSpace(r.Answer), "\n", 2)[0]
				if len(first) > 90 {
					first = first[:89] + "…"
				}
				line += " — " + first
			}
			evs = append(evs, timelineEvent{r.StartedAt, "⏎", line, styles.Header()})
		}
	}
	evs = append(evs, scalingTimeline(t.scaling)...)
	evs = append(evs, blastEvents(evs)...)
	sort.SliceStable(evs, func(a, b int) bool { return evs[a].at.Before(evs[b].at) })
	return evs
}

// blastEvents clusters alert-FIRED events: ≥2 distinct jobs inside a 2-minute
// window synthesize a blast-radius marker.
func blastEvents(evs []timelineEvent) []timelineEvent {
	type fired struct {
		at  time.Time
		job string
	}
	var fires []fired
	for _, e := range evs {
		if e.glyph != "⚠" {
			continue
		}
		parts := strings.SplitN(e.text, " · ", 2)
		if len(parts) < 2 {
			continue
		}
		job := strings.SplitN(parts[1], " / ", 2)[0]
		fires = append(fires, fired{e.at, job})
	}
	sort.Slice(fires, func(a, b int) bool { return fires[a].at.Before(fires[b].at) })
	var out []timelineEvent
	var lastMark time.Time
	for i, f := range fires {
		jobs := map[string]bool{f.job: true}
		for j := i + 1; j < len(fires) && fires[j].at.Sub(f.at) <= 2*time.Minute; j++ {
			jobs[fires[j].job] = true
		}
		if len(jobs) >= 2 && f.at.Sub(lastMark) > 10*time.Minute {
			lastMark = f.at
			names := make([]string, 0, len(jobs))
			for j := range jobs {
				if len(j) > 26 {
					j = j[:25] + "…"
				}
				names = append(names, j)
			}
			sort.Strings(names)
			out = append(out, timelineEvent{f.at, "⛓",
				fmt.Sprintf("BLAST RADIUS: %d jobs firing within 2m (%s) — likely shared cause", len(jobs), strings.Join(names, ", ")), styles.ErrText()})
		}
	}
	return out
}

func (t *Timeline) render(width int) string {
	evs := t.collect()
	if len(evs) == 0 {
		return emptyState(" Nothing yet. Alerts, savepoints/stops, and AI investigations land\n here as they happen — the incident's causal chain in one view.")
	}
	var b strings.Builder
	lastDay := ""
	for _, e := range evs {
		day := e.at.Format("2006-01-02")
		if day != lastDay {
			b.WriteString(styles.Dim().Render(" ── "+day+" ──") + "\n")
			lastDay = day
		}
		b.WriteString(styles.Dim().Render(e.at.Format(" 15:04:05 ")) + e.sty.Render(e.glyph) + " " + e.text + "\n")
	}
	return b.String()
}

func (t *Timeline) Update(msg tea.Msg) (Model, tea.Cmd) {
	if sm, ok := msg.(timelineScalingMsg); ok {
		t.scaling = sm.evs
		return t, nil
	}
	if km, ok := msg.(tea.KeyMsg); ok {
		if km.String() == "E" {
			path, err := t.exportReport()
			if err != nil {
				return t, flashErrV("export: " + err.Error())
			}
			_, _ = osc52.New(path).WriteTo(os.Stderr)
			return t, flash("postmortem draft written: " + path + " (path copied)")
		}
		if t.ready {
			var cmd tea.Cmd
			t.vp, cmd = t.vp.Update(km)
			return t, cmd
		}
	}
	return t, nil
}

// exportReport writes the incident as a markdown postmortem draft —
// timeline, alert summary, actions taken, and AI findings — ready to paste
// into the incident channel before the retro is scheduled.
func (t *Timeline) exportReport() (string, error) {
	evs := t.collect()
	var b strings.Builder
	now := time.Now()
	fmt.Fprintf(&b, "# Incident report — context %s\n\n", t.deps.Ctx)
	fmt.Fprintf(&b, "_Draft generated by f9s at %s_\n\n", now.Format("2006-01-02 15:04:05"))
	if len(evs) > 0 {
		fmt.Fprintf(&b, "**Window:** %s → %s\n\n",
			evs[0].at.Format("2006-01-02 15:04:05"), evs[len(evs)-1].at.Format("15:04:05"))
	}

	b.WriteString("## Alerts\n\n")
	n := 0
	if s := t.deps.Sentinel; s != nil {
		for _, al := range s.OpenAlerts() {
			fmt.Fprintf(&b, "- **STILL FIRING** · %s · %s / %s · since %s · peak %s\n",
				al.Kind, al.Job, al.Vertex, al.OpenedAt.Format("15:04:05"), al.Peak)
			n++
		}
		for _, al := range s.Episodes() {
			fmt.Fprintf(&b, "- resolved · %s · %s / %s · %s–%s (%s) · peak %s\n",
				al.Kind, al.Job, al.Vertex, al.OpenedAt.Format("15:04:05"),
				al.ClosedAt.Format("15:04:05"), al.ClosedAt.Sub(al.OpenedAt).Round(time.Second), al.Peak)
			n++
		}
	}
	if n == 0 {
		b.WriteString("- none recorded\n")
	}

	b.WriteString("\n## Actions taken\n\n")
	if entries, err := ledger.List(t.deps.Ctx); err == nil && len(entries) > 0 {
		for _, e := range entries {
			fmt.Fprintf(&b, "- %s · %s · %s → `%s`\n", e.Time.Format("15:04:05"), e.Action, e.Job, e.Path)
		}
	} else {
		b.WriteString("- none recorded\n")
	}

	b.WriteString("\n## AI findings\n\n")
	m := 0
	if w := t.deps.Watch; w != nil {
		for _, r := range w.List() {
			fmt.Fprintf(&b, "### %s (%s, %s)\n\n", r.Job, r.Trigger, r.StartedAt.Format("15:04:05"))
			if r.Err != "" {
				fmt.Fprintf(&b, "investigation failed: %s\n\n", r.Err)
			} else {
				fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(r.Answer))
			}
			m++
		}
	}
	if m == 0 {
		b.WriteString("none this session (full audit trails live in ~/.local/share/f9s/investigations/)\n")
	}

	b.WriteString("\n## Timeline\n\n")
	for _, e := range evs {
		fmt.Fprintf(&b, "- `%s` %s %s\n", e.at.Format("15:04:05"), e.glyph, e.text)
	}

	dir := reportsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, now.Format("20060102-150405")+"-"+t.deps.Ctx+".md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// reportsDir is $XDG_DATA_HOME/f9s/reports (default ~/.local/share/...).
func reportsDir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "reports"
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "f9s", "reports")
}

func (t *Timeline) View(width, height int) string {
	return framed(width, height, "Timeline", -1, t.deps.Ctx+" · alerts · actions · autoscaler · investigations, merged", func(iw, ih int) string {
		if !t.ready {
			t.vp = viewport.New(iw, ih)
			t.ready = true
			t.vp.SetContent(t.render(iw))
			t.vp.GotoBottom()
		}
		t.vp.Width = iw
		t.vp.Height = ih
		atBottom := t.vp.AtBottom()
		t.vp.SetContent(t.render(iw))
		if atBottom {
			t.vp.GotoBottom()
		}
		return t.vp.View()
	})
}
