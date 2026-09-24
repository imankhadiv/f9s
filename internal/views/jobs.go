package views

import (
	"fmt"
	"os"
	"time"

	"encoding/json"
	"github.com/aymanbagabas/go-osc52/v2"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/imankhadiv/f9s/internal/plugin"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/sentinel"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type jobsMsg struct {
	owner *Jobs
	gen   int
	jobs  []model.JobOverview
	err   error
}

// jobExtra is the slow-path per-job data (checkpoint health, restarts)
// fetched alongside the jobs list, backpressure-style.
type jobExtra struct {
	lastCkpt  string
	ckptState string // COMPLETED / FAILED / IN_PROGRESS / ""
	restarts  string
	fetchedAt time.Time
}

type jobExtraMsg struct {
	owner *Jobs
	jid   string
	extra jobExtra
}

// verdict summarises the cluster for the top of the list: the landing
// screen should answer "is anything wrong" before the operator reads rows.
// History views describe completed jobs, so they get no verdict.
func (j *Jobs) verdict() (string, string) {
	if j.title == "history" {
		return "", ""
	}
	var alerts []sentinel.Alert
	if j.deps.Sentinel != nil {
		alerts = j.deps.Sentinel.OpenAlerts()
	}
	return jobsVerdict(j.all, j.extras, alerts)
}

// Jobs is the default view: every job on the cluster, problems first.
type Jobs struct {
	deps     Deps
	tbl      *uitable.Model
	all      []model.JobOverview
	filtered []model.JobOverview
	extras   map[string]jobExtra
	filter   textinput.Model
	filterOn bool
	sortMode int
	gen      int // fetch generation: stale responses are dropped (DESIGN.md #12)
	applied  int
	title    string
	err      string
}

// sort modes cycled by <o>: triage (problems first), name, uptime, status.
const (
	sortTriage = iota
	sortName
	sortUptime
	sortStatus
	sortModes
)

func sortLabel(mode int) string {
	switch mode {
	case sortName:
		return "name"
	case sortUptime:
		return "uptime"
	case sortStatus:
		return "status"
	default:
		return "triage"
	}
}

// NewJobs builds the jobs view.
func NewJobs(deps Deps) *Jobs {
	tbl := newTable([]uitable.Column{
		{Title: "NAME", Width: -1},
		{Title: "JID", Width: 10},
		{Title: "STATUS", Width: 12},
		{Title: "UPTIME", Width: 9, Right: true},
		{Title: "TASKS", Width: 9, Right: true},
		{Title: "LAST CKPT", Width: 10, Right: true},
		{Title: "RESTARTS", Width: 8, Right: true},
	})
	fi := textinput.New()
	fi.Prompt = "/"
	fi.CharLimit = 64
	return &Jobs{deps: deps, tbl: tbl, extras: map[string]jobExtra{}, filter: fi, title: "jobs"}
}

// NewHistoryJobs is the jobs view pointed at a History Server: completed
// jobs with the same keybindings and drill-downs (the HS mirrors the API).
func NewHistoryJobs(deps Deps) *Jobs {
	j := NewJobs(deps)
	j.title = "history"
	return j
}

// newTable builds a uitable with the shared f9s styling.
func newTable(cols []uitable.Column) *uitable.Model {
	t := uitable.New(cols)
	t.HeaderStyle = styles.TableHeader()
	t.BorderStyle = styles.TableBorder()
	t.SelectedStyle = styles.SelectedRow()
	return t
}

func (j *Jobs) Title() string   { return j.title }
func (j *Jobs) Filtering() bool { return j.filterOn }
func (j *Jobs) Bindings() []key.Binding {
	bs := []key.Binding{
		// primary — the first maxFooterKeys of these show in the footer bar;
		// the rest are in the ? full-help overlay.
		binding("enter", "enter", "detail"),
		binding("a", "a", "AI"),
		binding("c", "c", "ckpts"),
		binding("x", "x", "exceptions"),
		binding("s", "s", "savepoint"),
		binding("/", "/", "filter"),
		binding("o", "o", "sort"),
		binding("t", "t", "taskmanagers"),
		binding("m", "m", "metrics"),
		// secondary — ? only
		binding("S", "S", "stop+savepoint"),
		binding("ctrl+x", "ctrl-x", "cancel"),
		binding("p", "p", "ledger"),
		binding("C", "C", "contexts"),
		binding("K", "K", "k8s deployments"),
		binding("Q", "Q", "sql console"),
		binding("l", "l", "JM log"),
		binding("L", "L", "job logs (merged TMs)"),
		binding("h", "h", "archaeology"),
		binding("P", "P", "cluster pulse"),
		binding("y", "y", "describe"),
		binding("Y", "Y", "copy jid"),
	}
	for _, pl := range j.deps.Plugins {
		bs = append(bs, binding(pl.Key, pl.Key, pl.Description))
	}
	return bs
}

func (j *Jobs) Init() tea.Cmd { return j.Refresh() }

func (j *Jobs) Refresh() tea.Cmd {
	cl := j.deps.CL
	j.gen++
	gen := j.gen
	return func() tea.Msg {
		jobs, err := cl.Jobs()
		return jobsMsg{owner: j, gen: gen, jobs: jobs, err: err}
	}
}

// extraCmds fetches checkpoint health and restart count per live job.
func (j *Jobs) extraCmds() tea.Cmd {
	cl := j.deps.CL
	cmds := make([]tea.Cmd, 0, len(j.all))
	for _, job := range j.all {
		if job.State != "RUNNING" && job.State != "RESTARTING" && job.State != "FAILING" {
			continue
		}
		jid := job.JID
		cmds = append(cmds, func() tea.Msg {
			extra := jobExtra{lastCkpt: "-", restarts: "-"}
			if cp, err := cl.Checkpoints(jid); err == nil && len(cp.History) > 0 {
				h := cp.History[0]
				extra.ckptState = h.Status
				switch h.Status {
				case "COMPLETED":
					extra.lastCkpt = "✓ " + model.FmtAge(h.TriggerTimestamp)
				case "FAILED":
					extra.lastCkpt = "✗ " + model.FmtAge(h.TriggerTimestamp)
				default:
					extra.lastCkpt = "… " + model.FmtAge(h.TriggerTimestamp)
				}
			}
			if ms, err := cl.JobMetrics(jid, "numRestarts"); err == nil {
				for _, m := range ms {
					if m.ID == "numRestarts" && m.Value != "" {
						extra.restarts = m.Value
					}
				}
			}
			return jobExtraMsg{owner: j, jid: jid, extra: extra}
		})
	}
	return tea.Batch(cmds...)
}

func (j *Jobs) selected() (model.JobOverview, bool) {
	if len(j.filtered) == 0 {
		return model.JobOverview{}, false
	}
	return j.filtered[j.tbl.Cursor()], true
}

func (j *Jobs) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case jobsMsg:
		if msg.owner != j {
			return j, nil // result belongs to a view replaced by navigation/context switch
		}
		if msg.gen < j.applied {
			return j, nil // out-of-order response from an older tick
		}
		j.applied = msg.gen
		if msg.err != nil {
			j.err = msg.err.Error()
			return j, nil
		}
		j.err = ""
		j.all = msg.jobs
		live := map[string]bool{}
		for _, jb := range msg.jobs {
			live[jb.JID] = true
		}
		for jid := range j.extras {
			if !live[jid] {
				delete(j.extras, jid) // churned jobs must not leak
			}
		}
		j.applyFilter()
		return j, j.extraCmds()

	case jobExtraMsg:
		if msg.owner != j {
			return j, nil
		}
		e := msg.extra
		e.fetchedAt = time.Now()
		j.extras[msg.jid] = e
		j.applyFilter()
		return j, nil

	case tea.KeyMsg:
		if j.filterOn {
			switch msg.String() {
			case "enter", "esc":
				if msg.String() == "esc" {
					j.filter.SetValue("")
				}
				j.filterOn = false
				j.filter.Blur()
				j.applyFilter()
				return j, nil
			}
			var cmd tea.Cmd
			j.filter, cmd = j.filter.Update(msg)
			j.applyFilter()
			return j, cmd
		}
		switch msg.String() {
		case "/":
			j.filterOn = true
			return j, j.filter.Focus()
		case "enter":
			if job, ok := j.selected(); ok {
				return j, push(NewJobDetail(j.deps, job.JID, job.Name))
			}
		case "h":
			if job, ok := j.selected(); ok {
				return j, push(NewArchaeology(j.deps, job.Name))
			}
		case "P":
			return j, push(NewPulse(j.deps))
		case "y":
			if job, ok := j.selected(); ok {
				return j, push(NewDescribe(j.deps, job.Name, job))
			}
		case "Y":
			if job, ok := j.selected(); ok {
				_, _ = osc52.New(job.JID).WriteTo(os.Stderr)
				return j, flash("jid copied: " + job.JID)
			}
		case "l":
			return j, push(NewJMLog(j.deps))
		case "L":
			if job, ok := j.selected(); ok {
				return j, push(NewJobLogs(j.deps, job.JID, job.Name))
			}
		case "c":
			if job, ok := j.selected(); ok {
				return j, push(NewCheckpoints(j.deps, job.JID, job.Name))
			}
		case "x":
			if job, ok := j.selected(); ok {
				return j, push(NewExceptions(j.deps, job.JID, job.Name))
			}
		case "s":
			if g := readOnlyGuard(j.deps); g != nil {
				return j, g
			}
			if job, ok := j.selected(); ok {
				if job.State != "RUNNING" {
					return j, flashErrV("savepoint needs a RUNNING job; " + job.JID[:8] + " is " + job.State)
				}
				return j, requestAction(ActionSavepoint, job.JID, job.Name)
			}
		case "S":
			if g := readOnlyGuard(j.deps); g != nil {
				return j, g
			}
			if job, ok := j.selected(); ok {
				if job.State != "RUNNING" {
					return j, flashErrV("stop+savepoint needs a RUNNING job; " + job.JID[:8] + " is " + job.State)
				}
				return j, requestAction(ActionStop, job.JID, job.Name)
			}
		case "ctrl+x":
			if g := readOnlyGuard(j.deps); g != nil {
				return j, g
			}
			if job, ok := j.selected(); ok {
				return j, requestAction(ActionCancel, job.JID, job.Name)
			}
		case "p":
			return j, push(NewSavepoints(j.deps))
		case "t":
			return j, push(NewTaskManagers(j.deps))
		case "C":
			return j, push(NewContexts(j.deps))
		case "K":
			return j, push(NewKubeDeploys(j.deps))
		case "m":
			job, _ := j.selected()
			return j, push(NewMetrics(j.deps, job))
		case "a":
			if job, ok := j.selected(); ok {
				return j, push(NewAIMenu(j.deps, job.JID, job.Name))
			}
		case "Q":
			url := j.deps.Cfg.Contexts[j.deps.Ctx].SQLGateway
			if url == "" {
				return j, flash("no sqlGateway configured for context " + j.deps.Ctx + " (add sqlGateway: http://host:8083)")
			}
			return j, push(NewSQLConsole(j.deps, url))
		case "o":
			j.sortMode = (j.sortMode + 1) % sortModes
			j.applyFilter()
			return j, nil
		default:
			for _, pl := range j.deps.Plugins {
				if msg.String() == pl.Key {
					if job, ok := j.selected(); ok {
						return j, j.runPlugin(pl, job)
					}
					return j, nil
				}
			}
			j.tbl.Update(msg)
		}
	}
	return j, nil
}

// runPlugin builds the JSON payload for the selected job and asks the root
// app to execute the plugin.
func (j *Jobs) runPlugin(pl plugin.Plugin, job model.JobOverview) tea.Cmd {
	payload, err := json.Marshal(map[string]any{
		"context": j.deps.Ctx,
		"url":     j.deps.Cfg.Contexts[j.deps.Ctx].URL,
		"job":     job,
	})
	if err != nil {
		return flash("plugin payload: " + err.Error())
	}
	return func() tea.Msg { return PluginMsg{P: pl, Payload: payload} }
}

// triageRank floats problems to the top of the table.
func triageRank(state string) int {
	switch state {
	case "FAILING", "RESTARTING", "RECONCILING":
		return 0
	case "FAILED":
		return 1
	case "RUNNING", "CREATED", "INITIALIZING":
		return 2
	default: // FINISHED, CANCELED, ...
		return 3
	}
}

func (j *Jobs) applyFilter() {
	sort.SliceStable(j.all, func(a, b int) bool {
		ja, jb := j.all[a], j.all[b]
		switch j.sortMode {
		case sortName:
			return ja.Name < jb.Name
		case sortUptime: // newest deployments first
			return ja.Duration < jb.Duration
		case sortStatus:
			return ja.State < jb.State
		default:
			ra, rb := triageRank(ja.State), triageRank(jb.State)
			if ra != rb {
				return ra < rb
			}
			return ja.Name < jb.Name
		}
	})
	needle := strings.ToLower(j.filter.Value())
	j.filtered = j.filtered[:0]
	rows := make([][]uitable.Cell, 0, len(j.all))
	tints := map[int]lipgloss.Style{}
	var keys []string
	for _, job := range j.all {
		if needle != "" && !strings.Contains(strings.ToLower(job.Name), needle) {
			continue
		}
		j.filtered = append(j.filtered, job)
		extra := j.extras[job.JID]
		if extra.lastCkpt == "" {
			extra = jobExtra{lastCkpt: "-", restarts: "-"}
		}
		staleAfter := 2 * time.Duration(max(j.deps.Cfg.RefreshRate, 2)) * time.Second
		stale := !extra.fetchedAt.IsZero() && time.Since(extra.fetchedAt) > staleAfter
		if stale {
			extra.lastCkpt += " ⌛"
		}
		tasksStyle := styles.OKText()
		if job.Tasks.Failed > 0 {
			tasksStyle = styles.ErrText()
		}
		restartStyle := styles.Dim()
		if extra.restarts != "-" && extra.restarts != "0" {
			restartStyle = styles.ErrText()
		}
		keys = append(keys, job.JID)
		ckptStyle := styles.StateStyle(extra.ckptState)
		if stale {
			ckptStyle = styles.Dim()
			restartStyle = styles.Dim()
		}
		rows = append(rows, []uitable.Cell{
			uitable.Plain(job.Name),
			uitable.Styled(model.ShortJID(job.JID), styles.Dim()),
			uitable.Styled(job.State, styles.StateStyle(job.State)),
			uitable.Plain(model.FmtDuration(job.Duration)),
			uitable.Styled(statusTasks(job.Tasks), tasksStyle),
			uitable.Styled(extra.lastCkpt, ckptStyle),
			uitable.Styled(extra.restarts, restartStyle),
		})
		if st, ok := rowTint(job.State); ok {
			tints[len(rows)-1] = st
		}
	}
	j.tbl.SetRowsKeyed(rows, keys)
	j.tbl.SetRowTints(tints)
}

// rowTint fades terminal/dead jobs and flags failing ones so a job list reads
// as healthy/sick at a glance; RUNNING rows keep their per-cell colors.
func rowTint(state string) (lipgloss.Style, bool) {
	switch state {
	case "CANCELED", "FINISHED", "SUSPENDED", "COMPLETED":
		return styles.Dim(), true
	case "FAILED", "FAILING":
		return styles.ErrText(), true
	case "RESTARTING", "RECONCILING", "CANCELLING":
		return styles.WarnText(), true
	}
	return lipgloss.Style{}, false
}

func statusTasks(t model.TaskCounts) string {
	s := ""
	if t.Failed > 0 {
		s = "!"
	}
	return s + strconv.Itoa(t.Running) + "/" + strconv.Itoa(t.Total)
}

// AIContext summarizes the on-screen, triage-sorted job inventory for the AI panel.
func (j *Jobs) AIContext() (string, []string) {
	j.applyFilter()
	var b strings.Builder
	fmt.Fprintf(&b, "## On screen: %d jobs (sort: %s)\n", len(j.filtered), sortLabel(j.sortMode))
	if len(j.filtered) == 0 {
		b.WriteString("no jobs on this cluster\n")
		return b.String(), nil
	}
	names := make([]string, 0, len(j.filtered))
	shown := 0
	for _, job := range j.filtered {
		names = append(names, job.Name)
		if shown >= 15 {
			continue
		}
		shown++
		extra := j.extras[job.JID]
		restarts := extra.restarts
		if restarts == "" {
			restarts = "-"
		}
		ckpt := extra.lastCkpt
		if ckpt == "" {
			ckpt = "-"
		}
		fmt.Fprintf(&b, "  %s · %s · tasks %s · restarts %s · last-ckpt %s\n",
			job.Name, job.State, statusTasks(job.Tasks), restarts, ckpt)
	}
	if shown < len(j.filtered) {
		fmt.Fprintf(&b, "  … and %d more\n", len(j.filtered)-shown)
	}
	return b.String(), names
}

func (j *Jobs) View(width, height int) string {
	j.applyFilter() // re-render each frame so staleness markers age live
	label := "Jobs"
	if j.title == "history" {
		label = "Completed jobs"
	}
	col := -1
	switch j.sortMode {
	case sortName:
		col = 0
	case sortStatus:
		col = 2
	case sortUptime:
		col = 3
	}
	j.tbl.SetSort(col, styles.SortDesc)
	return framed(width, height, label, j.tbl.Len(), "sort: "+sortLabel(j.sortMode), func(iw, ih int) string {
		out := ""
		used := 0
		if j.filterOn || j.filter.Value() != "" {
			out += j.filter.View() + "\n"
			used++
		}
		if txt, level := j.verdict(); txt != "" {
			style := styles.OKText()
			if level == "crit" {
				style = styles.ErrText()
			}
			out += " " + style.Render(truncate(txt, max(10, iw-2))) + "\n"
			used++
		}
		if j.err != "" {
			out += errorBanner(j.err)
			used++
		}
		if j.tbl.Len() == 0 {
			if j.filter.Value() != "" {
				return out + emptyState("0 jobs match filter "+strconv.Quote(j.filter.Value()))
			}
			return centered(iw, ih, emptyState("No jobs on this cluster.\nSubmit one and it appears here within a refresh."))
		}
		j.tbl.SetSize(iw, ih-used)
		return out + j.tbl.View()
	})
}
