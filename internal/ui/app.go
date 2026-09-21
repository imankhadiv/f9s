// Package ui hosts the root bubbletea model: the view stack, the poll tick,
// the cluster header, global keybindings, and job-action execution.
package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/ai"
	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/audit"
	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/fleet"
	"github.com/imankhadiv/f9s/internal/incident"
	"github.com/imankhadiv/f9s/internal/invlog"
	"github.com/imankhadiv/f9s/internal/ledger"
	"github.com/imankhadiv/f9s/internal/metricts"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/plugin"
	"github.com/imankhadiv/f9s/internal/runbook"
	"github.com/imankhadiv/f9s/internal/sentinel"
	"github.com/imankhadiv/f9s/internal/snapshot"
	"github.com/imankhadiv/f9s/internal/tools"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/views"
	"github.com/imankhadiv/f9s/internal/watch"
)

const (
	flashTTL     = 8 * time.Second
	opPollPeriod = 2 * time.Second
)

type tickMsg time.Time

type overviewMsg struct {
	ov  model.ClusterOverview
	err error
}

// triggeredMsg reports the start of an async savepoint/stop operation.
type triggeredMsg struct {
	kind  views.ActionKind
	jid   string
	name  string
	reqID string
	err   error
}

// opStatusMsg is one poll result for a pending operation.
type opStatusMsg struct {
	reqID string
	st    model.SavepointStatus
	err   error
}

type cancelDoneMsg struct {
	name string
	err  error
}

type pluginDoneMsg struct {
	name   string
	output string
	err    error
}

type proposalDoneMsg struct {
	source string
	tool   string
	args   map[string]string
	output string
	err    error
}

type rescaleDoneMsg struct {
	vertex string
	p      int
	err    error
}

type pendingOp struct {
	kind  views.ActionKind
	jid   string
	name  string
	reqID string
}

// App is the root model.
type App struct {
	cfg          *config.Config
	plugins      []plugin.Plugin
	cl           *client.Client
	contextName  string
	savepointDir string
	refresh      time.Duration
	stack        []views.Model
	width        int
	height       int
	ov           model.ClusterOverview
	modeInfo     model.ModeInfo // detected deployment mode (application/session/…)
	connected    bool
	everSeen     bool
	lastOK       time.Time
	showHelp     bool
	modal        *modal
	op           *pendingOp
	flash        string
	flashErr     bool
	flashAt      time.Time
	flashSeq     int
	help         help.Model
	spin         spinner.Model
	fetching     bool
	cmdMode      bool
	cmdInput     textinput.Model
	sent         *sentinel.Sentinel
	metrics      *metricts.Ring
	alertHook    *plugin.Hook
	watchStore   *watch.Store
	invRunning   bool
	invQueue     []invRequest
	lastRestarts map[string]int
	sentRunning  bool
	failStreak   int   // consecutive overview failures driving backoff
	lastConnErr  error // last overview failure, for first-run diagnosis
	paletteMode  bool
	paletteInput textinput.Model
	paletteItems []paletteItem
	paletteSel   int
	lastBlast    time.Time      // debounce for blast-radius flashes
	notify       bool           // OSC 9 desktop notifications on alert open
	aiPanel      *views.AIPanel // persistent page-aware AI side panel
	aiOpen       bool           // panel visible (splits the body)
	aiFocused    bool           // key input routed to the panel
}

type invRequest struct {
	jid, job, trigger string
}

type invDoneMsg struct{ result watch.Result }

func newCmdInput() textinput.Model {
	in := textinput.New()
	in.Prompt = ":"
	in.CharLimit = 24
	return in
}

// commandTargets maps command-mode aliases to view constructors. Navigation
// commands reset the stack to jobs plus the target, so Esc always leads home.
var commandTargets = []struct {
	names []string
	desc  string
}{
	{[]string{"jobs", "job", "j"}, "jobs"},
	{[]string{"cluster", "cl"}, "cluster components (JM + TMs)"},
	{[]string{"tms", "tm", "taskmanagers"}, "taskmanagers"},
	{[]string{"savepoints", "sp", "ledger", "led", "s"}, "savepoint ledger"},
	{[]string{"contexts", "ctx"}, "contexts"},
	{[]string{"k8s", "kube", "deployments"}, "flinkdeployments"},
	{[]string{"sql"}, "sql console"},
	{[]string{"log", "calls"}, "rest call log"},
	{[]string{"alerts"}, "alerts"},
	{[]string{"investigations", "inv"}, "investigations"},
	{[]string{"config", "cfg"}, "cluster config"},
	{[]string{"jars", "jar"}, "upload & submit jars"},
	{[]string{"jmlog", "jml"}, "jobmanager log"},
	{[]string{"timeline", "tl"}, "incident timeline"},
	{[]string{"doctor", "doc"}, "cluster lint"},
	{[]string{"ask", "chat"}, "ask the AI (cluster-wide)"},
	{[]string{"pulse", "pls"}, "cluster heartbeat"},
	{[]string{"waste", "cost"}, "slot-waste finder"},
	{[]string{"fleet", "jvm", "mem"}, "JVM fleet: heap, GC, metaspace, buffers"},
	{[]string{"history", "hs"}, "history server"},
	{[]string{"quit", "q"}, "quit"},
}

// commandCandidates returns the distinct canonical commands matching input.
// An exact name or alias wins outright. Otherwise every name is prefix-matched
// and the hits are deduped by destination, so ambiguity is measured in places
// the user could land — ":ta" is unambiguous (only taskmanagers) while ":c" is
// not (cluster, contexts, config, ...).
func commandCandidates(input string) []string {
	if input == "" {
		return nil
	}
	for _, t := range commandTargets {
		for _, n := range t.names {
			if n == input {
				return []string{t.names[0]}
			}
		}
	}
	var matches []string
	seen := map[string]bool{}
	for _, t := range commandTargets {
		for _, n := range t.names {
			if strings.HasPrefix(n, input) && !seen[t.names[0]] {
				seen[t.names[0]] = true
				matches = append(matches, t.names[0])
			}
		}
	}
	return matches
}

// resolveCommand returns a canonical command only for an exact alias or a
// unique canonical prefix.
func resolveCommand(input string) string {
	matches := commandCandidates(input)
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func commandList() string {
	names := make([]string, len(commandTargets))
	for i, target := range commandTargets {
		names[i] = target.names[0]
	}
	return strings.Join(names, " · ")
}

func commandGuide(width int) string {
	return fitText("   "+commandList(), width)
}

// fitText truncates s to width columns, marking the cut with an ellipsis.
// Width <= 0 yields "" — there is no room to say anything.
func fitText(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes)+"…") > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// fitFlash keeps a flash line inside the terminal. The command list is the
// tail of every such message, so truncation drops guidance before it drops
// the error itself. Width 0 means the first WindowSizeMsg has not landed yet
// and nothing is being rendered, so the text passes through unbounded.
func (a *App) fitFlash(s string) string {
	if a.width <= 0 {
		return s
	}
	return fitText(s, a.width-1) // flashLine indents by one column
}

// deps is the one place views get their shared dependencies — every field,
// so no navigation path hands a view a partial set (a missing ring silently
// disables trends and estimates).
func (a *App) deps() views.Deps {
	return views.Deps{CL: a.cl, Ctx: a.contextName, Cfg: a.cfg, Plugins: a.plugins,
		Sentinel: a.sent, Watch: a.watchStore, Metrics: a.metrics}
}

// runCommand navigates to the resolved command's view.
func (a *App) runCommand(cmd string) tea.Cmd {
	deps := a.deps()
	jobs := views.NewJobs(deps)
	var target views.Model
	switch cmd {
	case "jobs":
		a.stack = []views.Model{jobs}
		return tea.Batch(jobs.Init())
	case "cluster":
		target = views.NewCluster(deps)
	case "tms":
		target = views.NewTaskManagers(deps)
	case "savepoints":
		target = views.NewSavepoints(deps)
	case "contexts":
		target = views.NewContexts(deps)
	case "k8s":
		target = views.NewKubeDeploys(deps)
	case "log":
		target = views.NewRestLog(deps)
	case "alerts":
		target = views.NewAlerts(deps)
	case "investigations":
		target = views.NewInvestigations(deps)
	case "config":
		target = views.NewJMConfig(deps)
	case "jars":
		target = views.NewJars(deps)
	case "jmlog":
		target = views.NewJMLog(deps)
	case "timeline":
		target = views.NewTimeline(deps)
	case "doctor":
		target = views.NewDoctor(deps)
	case "ask":
		a.stack = []views.Model{jobs}
		return tea.Batch(jobs.Init(), views.AskCluster(deps))
	case "pulse":
		target = views.NewPulse(deps)
	case "waste":
		target = views.NewWasteFinder(deps)
	case "fleet":
		target = views.NewFleet(deps)
	case "history":
		hsURL := a.cfg.Contexts[a.contextName].HistoryServer
		if hsURL == "" {
			return a.setFlash("no historyServer configured for context "+a.contextName+" (add historyServer: http://host:8082)", true)
		}
		hsDeps := deps
		hsDeps.CL = client.New(config.Context{URL: hsURL})
		target = views.NewHistoryJobs(hsDeps)
	case "sql":
		url := a.cfg.Contexts[a.contextName].SQLGateway
		if url == "" {
			return a.setFlash("no sqlGateway configured for context "+a.contextName, true)
		}
		target = views.NewSQLConsole(deps, url)
	case "quit":
		return tea.Quit
	default:
		return a.setFlash(a.fitFlash("unknown command :"+cmd+"  ("+commandList()+")"), true)
	}
	a.stack = []views.Model{jobs, target}
	return tea.Batch(jobs.Init(), target.Init())
}

// globalBindings are the app-level keys appended to every view's own.
func globalBindings() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "AI panel")),
		key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "back/quit")),
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	}
}

// New builds the root app with the jobs view on the stack.
func New(cfg *config.Config, contextName string, refreshSeconds int, plugins []plugin.Plugin, alertHook *plugin.Hook) *App {
	cctx := cfg.Contexts[contextName]
	cl := client.New(cctx)
	sent := sentinel.NewPersistent(contextName)
	ws := watch.NewStore()
	ring := metricts.New(metricts.BufferLen)
	deps := views.Deps{CL: cl, Ctx: contextName, Cfg: cfg, Plugins: plugins, Sentinel: sent, Watch: ws, Metrics: ring}
	a := &App{
		cfg:          cfg,
		cl:           cl,
		contextName:  contextName,
		savepointDir: cctx.SavepointDir,
		notify:       cctx.Notify,
		refresh:      time.Duration(refreshSeconds) * time.Second,
		plugins:      plugins,
		stack:        []views.Model{views.NewJobs(deps)},
		help:         help.New(),
		spin:         spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		cmdInput:     newCmdInput(),
		sent:         sent,
		metrics:      ring,
		alertHook:    alertHook,
		watchStore:   ws,
		lastRestarts: map[string]int{},
		aiPanel:      views.NewAIPanel(deps),
	}
	// the panel reads the current view's page context at ask time
	a.aiPanel.SetProvider(func() views.PageContext {
		label, ctx, names := views.AIContextOf(a.top())
		return views.PageContext{Label: label, Context: ctx, Names: names}
	})
	return a
}

// maybeInvestigate queues a watch-mode investigation for an opened alert.
func (a *App) maybeInvestigate(al sentinel.Alert) tea.Cmd {
	aiCfg := a.cfg.Contexts[a.contextName].AI
	if !aiCfg.Watch || aiCfg.Policy == "deny" {
		return nil
	}
	if !a.watchStore.ShouldInvestigate(al.Key.JID+"|"+al.Kind, time.Now()) {
		return nil
	}
	a.invQueue = append(a.invQueue, invRequest{jid: al.Key.JID, job: al.Job, trigger: al.Kind})
	return a.runNextInvestigation()
}

// runNextInvestigation starts the queued investigation (concurrency 1).
func (a *App) runNextInvestigation() tea.Cmd {
	if a.invRunning || len(a.invQueue) == 0 {
		return nil
	}
	req := a.invQueue[0]
	a.invQueue = a.invQueue[1:]
	a.invRunning = true
	cl := a.cl
	aiCfg := a.cfg.Contexts[a.contextName].AI
	reg := a.registry()
	anonymize := aiCfg.AnonymizeOn()
	ctxName := a.contextName
	return func() tea.Msg {
		res := watch.Result{StartedAt: time.Now(), Job: req.job, JID: req.jid, Trigger: req.trigger}
		lg := invlog.New(ctxName, req.jid, req.job, req.trigger)
		provider, err := ai.New(aiCfg)
		if err != nil {
			res.Err = err.Error()
			return invDoneMsg{result: res}
		}
		agent, ok := ai.AsAgent(provider)
		if !ok {
			res.Err = "provider has no agentic support"
			return invDoneMsg{result: res}
		}
		seed, err := analyze.BuildEvidence(cl, req.jid) // snapshot at fire time
		var fp incident.Fingerprint
		if err == nil {
			seed = runbook.Inject(seed, req.job, req.trigger)
			seed = incident.Inject(ctxName, seed, req.job, req.trigger)
			fp = incident.Extract(seed, req.job, req.trigger)
		}
		if err != nil {
			res.Err = "evidence: " + err.Error()
			return invDoneMsg{result: res}
		}
		scrubber := analyze.NewScrubber([]string{req.job})
		scrub := func(s string) string { return s }
		if anonymize {
			seed = scrubber.Scrub(seed)
			scrub = scrubber.Scrub
		}
		answer, err := agent.Investigate(context.Background(), seed, reg, scrub,
			func(line string) {
				lg.Line(line)
				res.Steps = append(res.Steps, line)
			},
			func(p ai.Proposal) {
				// background investigations are proposals-only, display-only
				lg.Line(fmt.Sprintf("PROPOSED (not run): %s %v", p.Tool, p.Args))
				res.Steps = append(res.Steps, fmt.Sprintf("PROPOSED (not run): %s %v", p.Tool, p.Args))
			})
		if err != nil {
			lg.Close("", err.Error())
			res.Err = err.Error()
			return invDoneMsg{result: res}
		}
		if anonymize {
			answer = scrubber.Restore(answer)
		}
		lg.Close(answer, "")
		incident.Append(ctxName, incident.Record{Time: time.Now(), JID: req.jid, FP: fp, Hypothesis: incident.HypothesisOf(answer)})
		res.Answer = answer
		return invDoneMsg{result: res}
	}
}

// checkBlastRadius flags concurrent alerts across DISTINCT jobs — the
// fastest tell that the cause is shared (Kafka, a sink, the cluster)
// rather than one job's own logic.
func (a *App) checkBlastRadius(now time.Time) tea.Cmd {
	if now.Sub(a.lastBlast) < 10*time.Minute {
		return nil
	}
	jobs := map[string]bool{}
	for _, al := range a.sent.OpenAlerts() {
		if now.Sub(al.OpenedAt) <= 2*time.Minute {
			jobs[al.Job] = true
		}
	}
	if len(jobs) < 2 {
		return nil
	}
	a.lastBlast = now
	names := make([]string, 0, len(jobs))
	for j := range jobs {
		if len(j) > 30 {
			j = j[:29] + "…"
		}
		names = append(names, j)
	}
	return a.setFlash(fmt.Sprintf("⛓ blast radius: %d jobs firing within 2m (%s) — likely shared cause",
		len(jobs), strings.Join(names, ", ")), true)
}

// notifyAlert emits an OSC 9 desktop notification and a terminal bell when
// notify is enabled — OSC to stderr passes through bubbletea's alt screen
// (the same path the OSC52 clipboard uses). Terminals that don't support
// OSC 9 (iTerm2/kitty/WezTerm do) simply ignore it.
func (a *App) notifyAlert(title, body string) {
	if !a.notify {
		return
	}
	fmt.Fprintf(os.Stderr, "\x1b]9;f9s — %s: %s\x07\a", title, body)
}

// fireAlertHook pipes an alert event to the user's onAlert command.
func (a *App) fireAlertHook(event string, al sentinel.Alert) tea.Cmd {
	if a.alertHook == nil {
		return nil
	}
	hook := *a.alertHook
	payload, err := json.Marshal(map[string]any{
		"event": event, "kind": al.Kind, "job": al.Job, "vertex": al.Vertex,
		"jid": al.Key.JID, "openedAt": al.OpenedAt, "context": a.contextName,
	})
	if err != nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, hook.Command, hook.Args...)
		cmd.Stdin = bytes.NewReader(payload)
		if out, err := cmd.CombinedOutput(); err != nil {
			return views.FlashMsg{Text: "onAlert hook: " + err.Error() + " " + strings.TrimSpace(string(out)), IsErr: true}
		}
		return nil
	}
}

const sentinelPeriod = 15 * time.Second

type sentinelTickMsg struct{}

type vertexSample struct {
	key         sentinel.Key
	job, vertex string
	sample      sentinel.Sample
	skew        int       // FLIP-418 score; -1 not applicable
	skewValues  []float64 // per-subtask records/s behind the score
}

type jobHealth struct {
	jid, job    string
	ckptBad     bool // last two checkpoints FAILED
	ckptKnown   bool
	numRestarts int // -1 unknown
	snap        *snapshot.Snap
}

type sentinelMsg struct {
	samples       []vertexSample
	jobs          []jobHealth
	liveJIDs      map[string]bool
	liveTMs       map[string]bool // nil when the TM list failed
	authoritative bool
}

func (a *App) sentinelTick() tea.Cmd {
	return tea.Tick(sentinelPeriod, func(time.Time) tea.Msg { return sentinelTickMsg{} })
}

// collectSentinel samples every running job's vertices (backpressure + busy).
func (a *App) collectSentinel() tea.Cmd {
	cl := a.cl
	ring := a.metrics
	return func() tea.Msg {
		jobs, err := cl.Jobs()
		if err != nil {
			return sentinelMsg{}
		}
		var out []vertexSample
		var health []jobHealth
		live := map[string]bool{}
		now := time.Now()
		for _, j := range jobs {
			if j.State != "RUNNING" && j.State != "RESTARTING" && j.State != "FAILING" {
				continue
			}
			live[j.JID] = true
			job, err := cl.Job(j.JID)
			if err != nil {
				continue
			}
			h := jobHealth{jid: j.JID, job: j.Name, numRestarts: -1}
			snap := snapshot.Snap{Time: now, JID: j.JID, Name: j.Name, Parallelism: map[string]int{}, FlinkVersion: cl.FlinkVersion()}
			for _, v := range job.Vertices {
				snap.Parallelism[v.Name] = v.Parallelism
			}
			if cc, err := cl.CheckpointConfig(j.JID); err == nil {
				snap.CkptInterval, snap.CkptTimeout = cc.Interval, cc.Timeout
				snap.Unaligned, snap.Backend = cc.UnalignedEnabled, cc.StateBackend
			}
			if jc, err := cl.JobConfig(j.JID); err == nil {
				snap.Restart = jc.ExecutionConfig.RestartStrategy
			}
			h.snap = &snap
			if cp, err := cl.Checkpoints(j.JID); err == nil {
				recordCheckpoint(ring, j.JID, cp)
				h.ckptKnown = true
				if len(cp.History) >= 2 {
					h.ckptBad = cp.History[0].Status == "FAILED" && cp.History[1].Status == "FAILED"
				}
			}
			if ms, err := cl.JobMetrics(j.JID, "numRestarts"); err == nil {
				for _, m := range ms {
					if m.ID == "numRestarts" {
						fmt.Sscanf(m.Value, "%d", &h.numRestarts)
					}
				}
				if h.numRestarts >= 0 {
					ring.Add(metricts.Key{Scope: metricts.ScopeJob, ID: j.JID, Metric: "numRestarts"},
						metricts.Point{Time: now, Value: float64(h.numRestarts)})
				}
			}
			health = append(health, h)
			for _, v := range job.Vertices {
				level := ""
				if bp, err := cl.Backpressure(j.JID, v.ID); err == nil {
					level = bp.Level()
				}
				busy := -1.0
				if b, err := cl.VertexBusy(j.JID, v.ID); err == nil {
					busy = b
				}
				skew := -1
				var skewValues []float64
				if v.Parallelism >= 2 {
					if recs, err := cl.VertexSubtaskRecords(j.JID, v.ID, v.Parallelism); err == nil && len(recs) > 0 {
						skew = analyze.SkewScore(recs)
						skewValues = recs
					}
				}
				vk := metricts.VertexID(j.JID, v.ID)
				if busy >= 0 {
					ring.Add(metricts.Key{Scope: metricts.ScopeVertex, ID: vk, Metric: metricts.Busy},
						metricts.Point{Time: now, Value: busy})
				}
				if skewValues != nil {
					var total float64
					for _, r := range skewValues {
						total += r
					}
					ring.Add(metricts.Key{Scope: metricts.ScopeVertex, ID: vk, Metric: metricts.RecordsIn},
						metricts.Point{Time: now, Value: total, Subtasks: skewValues})
				}
				out = append(out, vertexSample{
					key: sentinel.Key{JID: j.JID, VID: v.ID}, job: j.Name, vertex: v.Name,
					sample: sentinel.Sample{Time: now, Level: level, BusyMs: busy}, skew: skew, skewValues: skewValues,
				})
			}
		}
		liveTMs := fleet.Collect(cl, ring, now)
		return sentinelMsg{samples: out, jobs: health, liveJIDs: live, liveTMs: liveTMs, authoritative: true}
	}
}

// recordCheckpoint feeds the newest completed checkpoint into the ring once
// (keyed by its trigger time, so re-reading the same checkpoint adds nothing).
func recordCheckpoint(ring *metricts.Ring, jid string, cp model.Checkpoints) {
	for _, c := range cp.History {
		if c.Status != "COMPLETED" || c.IsSavepoint {
			continue
		}
		at := time.UnixMilli(c.TriggerTimestamp)
		size := metricts.Key{Scope: metricts.ScopeJob, ID: jid, Metric: metricts.CkptSize}
		if last, ok := ring.Last(size); ok && !at.After(last.Time) {
			return
		}
		ring.Add(size, metricts.Point{Time: at, Value: float64(c.StateSize)})
		ring.Add(metricts.Key{Scope: metricts.ScopeJob, ID: jid, Metric: metricts.CkptDuration},
			metricts.Point{Time: at, Value: float64(c.EndToEndDuration)})
		return
	}
}

func (a *App) top() views.Model { return a.stack[len(a.stack)-1] }

func (a *App) tick() tea.Cmd {
	return tea.Tick(a.effectiveRefresh(), func(t time.Time) tea.Msg { return tickMsg(t) })
}

// effectiveRefresh widens the poll interval exponentially (with jitter, cap
// 30s) while the cluster keeps failing, so a struggling JobManager sees a
// tool that backs off instead of a synchronized retry storm.
func (a *App) effectiveRefresh() time.Duration {
	if a.failStreak == 0 {
		return a.refresh
	}
	backoff := a.refresh * (1 << min(a.failStreak, 4)) // 2x..16x
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	// ±12% jitter breaks synchronization across instances
	jitter := time.Duration(rand.Int63n(int64(backoff / 4)))
	return backoff - backoff/8 + jitter
}

func (a *App) fetchOverview() tea.Cmd {
	cl := a.cl
	return func() tea.Msg {
		ov, err := cl.Overview()
		return overviewMsg{ov: ov, err: err}
	}
}

type modeMsg struct{ info model.ModeInfo }

// detectMode reads the cluster's deployment mode off the JobManager config in
// a goroutine (cached in the client after the first success).
func (a *App) detectMode() tea.Cmd {
	cl := a.cl
	return func() tea.Msg { return modeMsg{info: cl.DeploymentMode()} }
}

type clearFlashMsg struct{ seq int }

// setFlash shows a transient status line and returns the cmd that clears it.
func (a *App) setFlash(msg string, isErr bool) tea.Cmd {
	a.flash, a.flashErr, a.flashAt = msg, isErr, time.Now()
	a.flashSeq++
	seq := a.flashSeq
	return tea.Tick(flashTTL, func(time.Time) tea.Msg { return clearFlashMsg{seq: seq} })
}

// startAction fires the confirmed action against the cluster.
func (a *App) startAction(kind views.ActionKind, jid, name, dir string) tea.Cmd {
	cl := a.cl
	switch kind {
	case views.ActionSavepoint:
		return func() tea.Msg {
			id, err := cl.TriggerSavepoint(jid, dir)
			return triggeredMsg{kind: kind, jid: jid, name: name, reqID: id, err: err}
		}
	case views.ActionStop:
		return func() tea.Msg {
			id, err := cl.StopWithSavepoint(jid, dir, false)
			return triggeredMsg{kind: kind, jid: jid, name: name, reqID: id, err: err}
		}
	default:
		return func() tea.Msg {
			return cancelDoneMsg{name: name, err: cl.CancelJob(jid)}
		}
	}
}

// startProposal executes a human-confirmed agent proposal via the real
// tool registry.
func (a *App) startProposal(msg views.ProposalMsg) tea.Cmd {
	reg := a.registry()
	src := msg.AuditSource()
	return func() tea.Msg {
		t, ok := reg.Get(msg.Tool)
		if !ok {
			return proposalDoneMsg{source: src, tool: msg.Tool, args: msg.Args, err: fmt.Errorf("unknown tool")}
		}
		out, err := t.Run(context.Background(), msg.Args)
		if len(out) > 120 {
			out = out[:120] + "…"
		}
		return proposalDoneMsg{source: src, tool: msg.Tool, args: msg.Args, output: out, err: err}
	}
}

// registry builds the tool registry for the active context, with the
// operator tools bound to its kubeContext (if any).
func (a *App) registry() *tools.Registry {
	return tools.New(a.cl, a.contextName, tools.WithKube(a.cfg.Contexts[a.contextName].KubeContext))
}

// startRescale verifies the adaptive-scheduler precondition, then applies
// FLIP-291 resource requirements.
func (a *App) startRescale(msg views.RescaleMsg, parallelism int) tea.Cmd {
	cl := a.cl
	return func() tea.Msg {
		ok, err := cl.AdaptiveScheduler()
		if err == nil && !ok {
			return rescaleDoneMsg{vertex: msg.Vertex, p: parallelism,
				err: fmt.Errorf("cluster is not running the adaptive scheduler (set jobmanager.scheduler: adaptive)")}
		}
		err = cl.SetVertexParallelism(msg.JID, msg.VID, parallelism)
		return rescaleDoneMsg{vertex: msg.Vertex, p: parallelism, err: err}
	}
}

// pollOp checks a pending operation after a short delay. Commands run in
// their own goroutine, so sleeping here never blocks the UI.
func (a *App) pollOp(op pendingOp) tea.Cmd {
	cl := a.cl
	return func() tea.Msg {
		time.Sleep(opPollPeriod)
		st, err := cl.SavepointStatus(op.jid, op.reqID)
		return opStatusMsg{reqID: op.reqID, st: st, err: err}
	}
}

// runPlugin executes an exec plugin. Background plugins run detached with
// their (trimmed) output flashed; foreground plugins suspend the TUI.
func (a *App) runPlugin(msg views.PluginMsg) tea.Cmd {
	env := append(os.Environ(),
		"F9S_CONTEXT="+a.contextName,
		"F9S_URL="+a.cfg.Contexts[a.contextName].URL,
	)
	if msg.P.Background {
		p := msg.P
		payload := msg.Payload
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, p.Command, p.Args...)
			cmd.Stdin = bytes.NewReader(payload)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
			if len(first) > 120 {
				first = first[:120] + "…"
			}
			return pluginDoneMsg{name: p.Name, output: first, err: err}
		}
	}
	cmd := exec.Command(msg.P.Command, msg.P.Args...)
	cmd.Stdin = bytes.NewReader(msg.Payload)
	cmd.Env = env
	name := msg.P.Name
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return pluginDoneMsg{name: name, err: err}
	})
}

// Init starts the poll loop and the first fetches.
func (a *App) Init() tea.Cmd {
	return tea.Batch(a.top().Init(), a.fetchOverview(), a.tick(), a.sentinelTick())
}

// Update routes messages: modal first, then lifecycle, then the top view.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.help.Width = msg.Width
		return a, nil

	case sentinelTickMsg:
		if a.sentRunning {
			// previous collection still in flight (slow cluster) — skip
			// this cycle instead of stacking a second fan-out on top.
			return a, a.sentinelTick()
		}
		a.sentRunning = true
		return a, tea.Batch(a.collectSentinel(), a.sentinelTick())

	case sentinelMsg:
		a.sentRunning = false
		var cmds []tea.Cmd
		live := msg.liveJIDs
		if live == nil {
			live = map[string]bool{}
		}
		if msg.authoritative {
			a.sent.Prune(live)
			liveTMs := msg.liveTMs
			a.metrics.Prune(func(k metricts.Key) bool {
				if k.Scope == metricts.ScopeTM {
					return liveTMs == nil || liveTMs[k.ID] // a failed TM list prunes nothing
				}
				jid := metricts.JobOf(k)
				return jid == "" || live[jid] // cluster-scope series outlive jobs
			})
		}
		for _, h := range msg.jobs {
			if h.snap == nil {
				continue
			}
			if changed, diffs := snapshot.AppendIfChanged(a.contextName, *h.snap); changed && diffs[0] != "first observed" {
				cmds = append(cmds, a.setFlash("⚙ config changed on "+h.job+": "+strings.Join(diffs, "; "), true))
			}
		}
		if flash := a.checkBlastRadius(time.Now()); flash != nil {
			cmds = append(cmds, flash)
		}
		a.watchStore.PruneRecent(time.Now())
		if msg.authoritative {
			for jid := range a.lastRestarts {
				if !live[jid] {
					delete(a.lastRestarts, jid)
				}
			}
		}
		now := time.Now()
		for _, h := range msg.jobs {
			k := sentinel.Key{JID: h.jid, VID: "job"}
			if o, c := a.sent.ObserveConditionKnown(k, "checkpoint", h.job, "checkpointing", h.ckptBad, h.ckptKnown, now); o != nil || c != nil {
				if o != nil {
					a.notifyAlert("checkpoint failures", o.Job)
					cmds = append(cmds, a.setFlash("⚠ checkpoint failures: "+o.Job+" (2+ consecutive)", true))
					if hc := a.fireAlertHook("opened", *o); hc != nil {
						cmds = append(cmds, hc)
					}
					if ic := a.maybeInvestigate(*o); ic != nil {
						cmds = append(cmds, ic)
					}
				}
				if c != nil {
					cmds = append(cmds, a.setFlash("resolved: checkpoints recovering on "+c.Job, false))
					if hc := a.fireAlertHook("closed", *c); hc != nil {
						cmds = append(cmds, hc)
					}
				}
			}
			if h.numRestarts >= 0 {
				last, seen := a.lastRestarts[h.jid]
				a.lastRestarts[h.jid] = h.numRestarts
				restarting := seen && h.numRestarts > last
				if o, c := a.sent.ObserveCondition(k, "restart-loop", h.job, "job", restarting, now); o != nil || c != nil {
					if o != nil {
						a.notifyAlert("restart loop", o.Job)
						cmds = append(cmds, a.setFlash("⚠ restart loop: "+o.Job+" keeps restarting", true))
						if hc := a.fireAlertHook("opened", *o); hc != nil {
							cmds = append(cmds, hc)
						}
						if ic := a.maybeInvestigate(*o); ic != nil {
							cmds = append(cmds, ic)
						}
					}
					if c != nil {
						cmds = append(cmds, a.setFlash("resolved: "+c.Job+" stable again", false))
					}
				}
			}
		}
		for _, vs := range msg.samples {
			opened, closed := a.sent.Observe(vs.key, vs.job, vs.vertex, vs.sample)
			if opened != nil {
				a.notifyAlert("backpressure", opened.Job+" / "+opened.Vertex)
				cmds = append(cmds, a.setFlash("⚠ backpressure alert: "+opened.Job+" / "+opened.Vertex, true))
				if hc := a.fireAlertHook("opened", *opened); hc != nil {
					cmds = append(cmds, hc)
				}
				if ic := a.maybeInvestigate(*opened); ic != nil {
					cmds = append(cmds, ic)
				}
			}
			if closed != nil {
				cmds = append(cmds, a.setFlash("resolved: backpressure on "+closed.Job+" / "+closed.Vertex, false))
				if hc := a.fireAlertHook("closed", *closed); hc != nil {
					cmds = append(cmds, hc)
				}
			}
			if vs.skew >= 0 {
				sOpened, sClosed := a.sent.ObserveSkew(vs.key, vs.job, vs.vertex, vs.skew, vs.skewValues, vs.sample.Time)
				if sOpened != nil {
					a.notifyAlert("data skew", sOpened.Job+" / "+sOpened.Vertex)
					cmds = append(cmds, a.setFlash(fmt.Sprintf("⚠ data skew alert: %s / %s (%s)", sOpened.Job, sOpened.Vertex, sOpened.Peak), true))
					if hc := a.fireAlertHook("opened", *sOpened); hc != nil {
						cmds = append(cmds, hc)
					}
					if ic := a.maybeInvestigate(*sOpened); ic != nil {
						cmds = append(cmds, ic)
					}
				}
				if sClosed != nil {
					cmds = append(cmds, a.setFlash("resolved: skew on "+sClosed.Job+" / "+sClosed.Vertex, false))
					if hc := a.fireAlertHook("closed", *sClosed); hc != nil {
						cmds = append(cmds, hc)
					}
				}
			}
		}
		return a, tea.Batch(cmds...)

	case tickMsg:
		a.fetching = true
		return a, tea.Batch(a.top().Refresh(), a.fetchOverview(), a.tick(), a.spin.Tick)

	case spinner.TickMsg:
		if !a.fetching {
			return a, nil // stop animating once the fetch lands
		}
		var cmd tea.Cmd
		a.spin, cmd = a.spin.Update(msg)
		return a, cmd

	case overviewMsg:
		a.fetching = false
		if msg.err != nil {
			a.connected = false
			a.failStreak++
			a.lastConnErr = msg.err
		} else {
			a.connected = true
			a.everSeen = true
			a.lastOK = time.Now()
			a.ov = msg.ov
			a.failStreak = 0
			if a.modeInfo.Mode == model.ModeUnknown {
				return a, a.detectMode() // one cheap config read, then cached
			}
		}
		return a, nil

	case modeMsg:
		a.modeInfo = msg.info
		return a, nil

	case views.PushMsg:
		a.stack = append(a.stack, msg.M)
		return a, msg.M.Init()

	case views.PopMsg:
		if len(a.stack) > 1 {
			a.stack = a.stack[:len(a.stack)-1]
		}
		return a, nil

	case views.ActionMsg:
		if a.op != nil {
			return a, a.setFlash(fmt.Sprintf("%s on %s still in progress — one operation at a time", verbOf(a.op.kind), a.op.name), true)
		}
		a.modal = newModal(msg.Kind, msg.JID, msg.Name, a.savepointDir, a.mode())
		return a, nil

	case invDoneMsg:
		a.invRunning = false
		a.watchStore.Add(msg.result)
		flashText := "investigation complete: " + msg.result.Job + " (" + msg.result.Trigger + ") — :inv to view"
		isErr := msg.result.Err != ""
		if isErr {
			flashText = "investigation failed: " + msg.result.Job + " — " + msg.result.Err
		}
		return a, tea.Batch(a.setFlash(flashText, isErr), a.runNextInvestigation())

	case views.ProposalMsg:
		// The proposal path (investigate + ask) is the one mutating flow that
		// bypasses readOnlyGuard — gate it here so a read-only context can
		// never execute an AI-proposed action, confirmed or not.
		if a.cfg.Contexts[a.contextName].ReadOnly {
			return a, a.setFlash("[RO] context is read-only — mutating actions cannot be executed", true)
		}
		// Record the AI-surfaced call with its REAL target and params before the
		// human decides — so the forensic trail shows what was proposed even if
		// it is declined (a "proposed" line with no matching execute = declined).
		audit.Append(audit.Record{Context: a.contextName, Action: msg.Tool, Target: tools.TargetOf(msg.Args), Params: msg.Args, Result: "proposed", Source: msg.AuditSource()})
		a.modal = newProposalModal(msg, a.mode())
		return a, nil

	case proposalDoneMsg:
		res := "ok"
		if msg.err != nil {
			res = msg.err.Error()
		}
		audit.Append(audit.Record{Context: a.contextName, Action: msg.tool, Target: tools.TargetOf(msg.args), Params: msg.args, Result: res, Source: msg.source})
		if msg.err != nil {
			return a, a.setFlash(msg.tool+": "+msg.err.Error(), true)
		}
		return a, tea.Batch(a.setFlash("applied "+msg.tool+" ✓ — "+msg.output, false), a.top().Refresh())

	case views.RescaleMsg:
		if a.cfg.Contexts[a.contextName].ReadOnly {
			return a, a.setFlash("[RO] this context is read-only — rescale disabled", true)
		}
		a.modal = newRescaleModal(msg)
		return a, nil

	case rescaleDoneMsg:
		res := "ok"
		if msg.err != nil {
			res = msg.err.Error()
		}
		audit.Append(audit.Record{Context: a.contextName, Action: "rescale", Target: msg.vertex,
			Params: map[string]string{"parallelism": fmt.Sprint(msg.p)}, Result: res, Source: "tui"})
		if msg.err != nil {
			return a, a.setFlash("rescale "+msg.vertex+": "+msg.err.Error(), true)
		}
		return a, tea.Batch(a.setFlash(fmt.Sprintf("rescale %s → %d requested ✓ — the adaptive scheduler applies it within ~a minute (the graph shows N → %d ⟳ until it does)", msg.vertex, msg.p, msg.p), false), a.top().Refresh())

	case views.PluginMsg:
		return a, a.runPlugin(msg)

	case pluginDoneMsg:
		if msg.err != nil {
			return a, a.setFlash("plugin "+msg.name+": "+msg.err.Error(), true)
		}
		if msg.output != "" {
			return a, a.setFlash("plugin "+msg.name+": "+msg.output, false)
		}
		return a, a.setFlash("plugin "+msg.name+" done", false)

	case views.SwitchContextMsg:
		cctx, ok := a.cfg.Contexts[msg.Name]
		if !ok {
			return a, a.setFlash("context "+msg.Name+" not found", true)
		}
		a.contextName = msg.Name
		a.savepointDir = cctx.SavepointDir
		a.cl = client.New(cctx)
		a.ov = model.ClusterOverview{}
		a.modeInfo = model.ModeInfo{} // re-detect for the new cluster
		a.connected, a.everSeen = false, false
		a.sent = sentinel.NewPersistent(msg.Name) // fresh watchdog for the new cluster
		a.watchStore = watch.NewStore()
		a.metrics = metricts.New(metricts.BufferLen)
		deps := a.deps()
		a.stack = []views.Model{views.NewJobs(deps)}
		a.aiPanel.Reset(deps) // fresh cluster → drop the old transcript/scrubber
		a.aiPanel.SetProvider(func() views.PageContext {
			label, ctx, names := views.AIContextOf(a.top())
			return views.PageContext{Label: label, Context: ctx, Names: names}
		})
		a.aiOpen, a.aiFocused = false, false
		return a, tea.Batch(a.top().Init(), a.fetchOverview(), a.setFlash("switched to context "+msg.Name, false))

	case views.FlashMsg:
		return a, a.setFlash(msg.Text, msg.IsErr)

	case clearFlashMsg:
		if msg.seq == a.flashSeq {
			a.flash = ""
		}
		return a, nil

	case triggeredMsg:
		if msg.err != nil {
			return a, a.setFlash(verbOf(msg.kind)+" "+msg.name+": "+msg.err.Error(), true)
		}
		op := pendingOp{kind: msg.kind, jid: msg.jid, name: msg.name, reqID: msg.reqID}
		a.op = &op
		return a, tea.Batch(a.setFlash(verbOf(msg.kind)+" "+msg.name+" IN_PROGRESS…", false), a.pollOp(op))

	case opStatusMsg:
		if a.op == nil || a.op.reqID != msg.reqID {
			return a, nil // stale poll for an operation we no longer track
		}
		if msg.err != nil {
			cmd := a.setFlash(verbOf(a.op.kind)+" "+a.op.name+": status check failed: "+msg.err.Error(), true)
			a.op = nil
			return a, cmd
		}
		if msg.st.Status.ID != "COMPLETED" {
			return a, a.pollOp(*a.op)
		}
		op := *a.op
		a.op = nil
		if msg.st.Failed() {
			cause := msg.st.Operation.FailureCause.Class
			if cause == "" {
				cause = "no savepoint location returned"
			}
			audit.Append(audit.Record{Context: a.contextName, Action: verbOf(op.kind), Target: op.name, Result: cause, Source: "tui"})
			return a, a.setFlash(verbOf(op.kind)+" "+op.name+" FAILED: "+cause, true)
		}
		loc := msg.st.Operation.Location
		action := "savepoint"
		if op.kind == views.ActionStop {
			action = "stop"
		}
		if err := ledger.Append(a.contextName, ledger.Entry{
			Time: time.Now(), JID: op.jid, Job: op.name, Action: action, Path: loc,
		}); err != nil {
			return a, tea.Batch(a.setFlash("savepoint done ("+loc+") but ledger write failed: "+err.Error(), true), a.top().Refresh())
		}
		audit.Append(audit.Record{Context: a.contextName, Action: verbOf(op.kind), Target: op.name,
			Params: map[string]string{"path": loc}, Result: "ok", Source: "tui"})
		return a, tea.Batch(a.setFlash(verbOf(op.kind)+" "+op.name+" ✓ → "+loc, false), a.top().Refresh())

	case cancelDoneMsg:
		res := "ok"
		if msg.err != nil {
			res = msg.err.Error()
		}
		audit.Append(audit.Record{Context: a.contextName, Action: "cancel", Target: msg.name, Result: res, Source: "tui"})
		var cmd tea.Cmd
		if msg.err != nil {
			cmd = a.setFlash("cancel "+msg.name+": "+msg.err.Error(), true)
		} else {
			cmd = a.setFlash("cancel "+msg.name+" requested ✓", false)
		}
		return a, tea.Batch(cmd, a.top().Refresh())

	case tea.KeyMsg:
		if a.showHelp {
			switch msg.String() {
			case "ctrl+c":
				return a, tea.Quit
			case "esc", "q", "?":
				a.showHelp = false
			}
			return a, nil
		}
		if a.paletteMode {
			return a.handlePaletteKey(msg)
		}
		if msg.String() == "ctrl+p" && a.modal == nil && !a.cmdMode {
			// views with their own ctrl+p (metrics pin) keep it when focused
			if _, isMetrics := a.top().(*views.MetricsExplorer); !isMetrics {
				a.paletteMode = true
				a.paletteItems = a.buildPalette()
				a.paletteSel = 0
				a.paletteInput = newCmdInput()
				a.paletteInput.Prompt = "▶ "
				a.paletteInput.CharLimit = 48
				a.paletteInput.Focus()
				return a, nil
			}
		}
		if a.cmdMode {
			switch msg.String() {
			case "esc":
				a.cmdMode = false
				a.cmdInput.SetValue("")
				a.cmdInput.Blur()
				return a, nil
			case "enter":
				input := strings.TrimSpace(a.cmdInput.Value())
				matches := commandCandidates(input)
				a.cmdMode = false
				a.cmdInput.SetValue("")
				a.cmdInput.Blur()
				switch len(matches) {
				case 0:
					return a, a.setFlash(a.fitFlash("unknown command :"+input+"  ("+commandList()+")"), true)
				case 1:
					return a, a.runCommand(matches[0])
				default:
					return a, a.setFlash(a.fitFlash("ambiguous command :"+input+"  (matches: "+strings.Join(matches, " · ")+")"), true)
				}
			}
			var cmd tea.Cmd
			a.cmdInput, cmd = a.cmdInput.Update(msg)
			return a, cmd
		}
		if a.modal != nil {
			closed, confirmed, cmd := a.modal.update(msg)
			if !closed {
				return a, cmd
			}
			m := a.modal
			a.modal = nil
			if !confirmed {
				return a, nil
			}
			if m.proposal != nil {
				p := *m.proposal
				if in := p.Input; in != nil {
					args := make(map[string]string, len(p.Args)+1)
					for k, v := range p.Args {
						args[k] = v
					}
					args[in.Arg] = strings.TrimSpace(m.input.Value())
					p.Args = args
				}
				return a, a.startProposal(p)
			}
			if m.rescale != nil {
				p, err := strconv.Atoi(strings.TrimSpace(m.input.Value()))
				if err != nil || p < 1 {
					return a, a.setFlash("rescale: parallelism must be a positive integer", true)
				}
				return a, a.startRescale(*m.rescale, p)
			}
			dir := ""
			if m.needsInput {
				dir = m.input.Value()
			}
			return a, a.startAction(m.kind, m.jid, m.name, dir)
		}
		// AI panel focused: it captures all typing (enter=send, digits=pick a
		// proposal, pgup/pgdn=scroll). Esc yields focus back to the main view.
		if a.aiOpen && a.aiFocused {
			if msg.String() == "esc" {
				a.aiFocused = false
				a.aiPanel.Blur()
				return a, nil
			}
			var cmd tea.Cmd
			a.aiPanel, cmd = a.aiPanel.Update(msg)
			return a, cmd
		}
		if !a.top().Filtering() {
			switch msg.String() {
			case "ctrl+c":
				return a, tea.Quit
			case "A":
				if !a.aiOpen {
					if a.width < aiMinTotal {
						return a, a.setFlash("terminal too narrow for the AI panel — widen to ~80 cols", true)
					}
					a.aiOpen, a.aiFocused = true, true
					return a, a.aiPanel.Focus()
				}
				a.aiOpen, a.aiFocused = false, false
				a.aiPanel.Blur()
				return a, nil
			case "tab":
				if a.aiOpen {
					a.aiFocused = !a.aiFocused
					if a.aiFocused {
						return a, a.aiPanel.Focus()
					}
					a.aiPanel.Blur()
					return a, nil
				}
			case "q":
				// q backs out one level; it only quits from the root view.
				if len(a.stack) > 1 {
					a.stack = a.stack[:len(a.stack)-1]
					return a, nil
				}
				return a, tea.Quit
			case ":":
				a.cmdMode = true
				return a, a.cmdInput.Focus()
			case "!":
				deps := a.deps()
				v := views.NewAlerts(deps)
				a.stack = append(a.stack, v)
				return a, v.Init()
			case "?":
				a.showHelp = !a.showHelp
				return a, nil
			case "esc":
				if len(a.stack) > 1 {
					a.stack = a.stack[:len(a.stack)-1]
					return a, nil
				}
				return a, nil
			case "r":
				a.fetching = true
				return a, tea.Batch(a.top().Refresh(), a.fetchOverview(), a.spin.Tick)
			}
		}
	}
	top, cmd := a.top().Update(msg)
	a.stack[len(a.stack)-1] = top
	// Non-key messages also reach the AI panel (its async Ask events stream in
	// this way). Keys never do here — the panel only takes keys when focused,
	// handled above — so an unfocused panel can't swallow the main view's input.
	if _, isKey := msg.(tea.KeyMsg); !isKey && a.aiPanel != nil {
		var pcmd tea.Cmd
		a.aiPanel, pcmd = a.aiPanel.Update(msg)
		return a, tea.Batch(cmd, pcmd)
	}
	return a, cmd
}

func verbOf(kind views.ActionKind) string {
	switch kind {
	case views.ActionSavepoint:
		return "savepoint"
	case views.ActionStop:
		return "stop+savepoint"
	default:
		return "cancel"
	}
}

// crumbs renders the drill-down trail once the user has drilled in; at the
// root it returns "" because the view's own title bar already names the
// screen (no point showing "jobs" above "JOBS").
func (a *App) crumbs() string {
	if len(a.stack) <= 1 {
		return ""
	}
	parts := make([]string, len(a.stack))
	for i, v := range a.stack {
		if i == len(a.stack)-1 {
			parts[i] = styles.Header().Render(v.Title())
		} else {
			parts[i] = styles.Dim().Render(v.Title())
		}
	}
	return " " + strings.Join(parts, styles.Dim().Render(" › "))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// flashLine renders the transient status line, or "" when idle.
func (a *App) flashLine() string {
	if a.flash == "" {
		return ""
	}
	if a.flashErr {
		return styles.ErrText().Render(" " + a.flash)
	}
	return styles.OKText().Render(" " + a.flash)
}

// View renders header, breadcrumbs, active view (or modal), flash, footer.
// firstRunDiagnosis replaces a blank first screen with an explanation and
// next steps when f9s has never reached the cluster — a stranger's broken
// first run must still build trust, not dump a stack trace.
func (a *App) firstRunDiagnosis() string {
	url := a.cfg.Contexts[a.contextName].URL
	var b strings.Builder
	b.WriteString("\n " + styles.ErrText().Render("f9s can't reach a Flink cluster yet.") + "\n\n")
	b.WriteString(styles.Dim().Render(" context:  ") + a.contextName + "\n")
	b.WriteString(styles.Dim().Render(" url:      ") + url + "\n")
	if a.lastConnErr != nil {
		b.WriteString(styles.Dim().Render(" error:    ") + a.lastConnErr.Error() + "\n")
	}
	b.WriteString("\n" + styles.OKText().Render(" things to try") + "\n")
	b.WriteString(styles.Dim().Render("  · no cluster yet?      ") + "make chaos" + styles.Dim().Render("  (demo cluster where every detector fires)") + "\n")
	b.WriteString(styles.Dim().Render("  · cluster elsewhere?   ") + "f9s --address http://host:8081" + styles.Dim().Render("  or edit ~/.config/f9s/config.yaml (f9s init writes a starter)") + "\n")
	b.WriteString(styles.Dim().Render("  · behind auth/TLS?     ") + "add an auth block to the context" + styles.Dim().Render("  (type: bearer, tokenEnv: MY_TOKEN)") + "\n")
	b.WriteString(styles.Dim().Render("  · multiple contexts?   ") + "press C to switch" + "\n")
	b.WriteString("\n" + styles.Dim().Render(" f9s keeps retrying with backoff — this screen replaces itself the moment the cluster answers."))
	return b.String()
}

func (a *App) View() string {
	if a.width == 0 {
		return "starting…"
	}
	if a.width < 60 || a.height < 10 {
		return "\n  f9s needs at least 60×10 — please enlarge the terminal."
	}
	if a.showHelp {
		overlay := "\n  f9s — a terminal UI for Apache Flink\n\n" +
			a.help.FullHelpView([][]key.Binding{a.top().Bindings(), globalBindings()}) +
			"\n\n  Actions always confirm before touching the cluster.\n" +
			"  Completed savepoint paths are recorded in the ledger (<p>)."
		return a.header() + "\n" + overlay
	}
	flash := a.flashLine()
	crumb := a.crumbs()
	// The tall k9s header carries the hotkeys itself, so the footer legend is
	// dropped in full mode; a command prompt still claims a bottom row.
	full := a.headerFull()
	headerH := 3
	if full {
		headerH = 3 + logoRows - 1 // rounded box (2) + 6 content rows
	}
	bottomBar := a.cmdMode || !full
	chrome := headerH
	if bottomBar {
		chrome++
	}
	if crumb != "" {
		chrome++ // breadcrumb trail (only when drilled in)
	}
	bodyH := a.height - chrome
	if flash != "" {
		bodyH--
	}
	var body string
	switch {
	case a.paletteMode:
		body = a.paletteView(bodyH)
	case a.modal != nil:
		body = a.modal.view(a.width, bodyH)
	case !a.everSeen && a.failStreak >= 2:
		body = a.firstRunDiagnosis()
	case a.aiOpen && a.width >= aiMinTotal:
		// split the body: main view on the left, AI panel on the right.
		panelW := aiPanelWidth(a.width)
		main := a.top().View(a.width-panelW, bodyH)
		panel := a.aiPanel.View(panelW, bodyH, a.aiFocused)
		body = lipgloss.JoinHorizontal(lipgloss.Top, main, panel)
	default:
		body = a.top().View(a.width, bodyH)
	}
	// Assemble top-to-bottom with no trailing newline, so the rendered height
	// matches the chrome budget exactly.
	out := a.header()
	if crumb != "" {
		out += "\n" + crumb
	}
	out += "\n" + body
	if flash != "" {
		out += "\n" + flash
	}
	if a.cmdMode {
		head := " " + a.cmdInput.View()
		// the candidate list grows with ambiguity (":c" reaches six views), so
		// fit it before styling — lipgloss.Width would otherwise be measuring
		// escape codes we cannot cut cleanly.
		avail := a.width - lipgloss.Width(head)
		hint := ""
		matches := commandCandidates(strings.TrimSpace(a.cmdInput.Value()))
		if len(matches) == 1 {
			hint = styles.OKText().Render(fitText("  → "+matches[0], avail))
		} else if len(matches) > 1 {
			hint = styles.WarnText().Render(fitText("  ? "+strings.Join(matches, " · "), avail))
		}
		line := head + hint
		return out + "\n" + line + styles.Dim().Render(commandGuide(a.width-lipgloss.Width(line)))
	}
	if !full {
		out += "\n" + a.keyLegend(a.top().Bindings(), globalBindings())
	}
	return out
}

// maxFooterKeys caps how many of a view's own hotkeys the footer advertises,
// so busy views (jobs has ~15) don't spew a wall of keys. The rest live in the
// full-help overlay (?); the globals are always shown.
const maxFooterKeys = 9

// AI panel width budget: the panel takes ~a third, clamped, and never starves
// the main view. The panel only opens at widths where both panes are safe.
const (
	aiPanelMin = 34
	aiPanelMax = 56
	aiMainMin  = 44
	aiMinTotal = aiMainMin + aiPanelMin // below this, the toggle refuses to open
)

func aiPanelWidth(total int) int {
	p := total / 3
	if p > aiPanelMax {
		p = aiPanelMax
	}
	if p < aiPanelMin {
		p = aiPanelMin
	}
	if total-p < aiMainMin {
		p = total - aiMainMin
	}
	if p < 20 {
		p = 20
	}
	return p
}

// keyLegend renders the footer hotkey bar k9s-style: each key in the brand
// accent, its description dim, dot-separated. It shows the view's first few
// keys (a "…" marks the rest, discoverable via ?) and always the globals.
func (a *App) keyLegend(view, global []key.Binding) string {
	seg := func(b key.Binding) (string, bool) {
		h := b.Help()
		if h.Key == "" {
			return "", false
		}
		s := styles.Accent().Render(h.Key)
		if h.Desc != "" {
			s += " " + styles.Dim().Render(h.Desc)
		}
		return s, true
	}
	var parts []string
	shown := 0
	for _, b := range view {
		if shown >= maxFooterKeys {
			parts = append(parts, styles.Dim().Render("… ? more"))
			break
		}
		if s, ok := seg(b); ok {
			parts = append(parts, s)
			shown++
		}
	}
	for _, b := range global { // globals always shown
		if s, ok := seg(b); ok {
			parts = append(parts, s)
		}
	}
	line := " " + strings.Join(parts, styles.Dim().Render(" · "))
	return lipgloss.NewStyle().MaxWidth(a.width).Render(line)
}
