package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/kube"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

// KubeDetail describes one operator resource: spec, reconcile state, drift,
// the full error, savepoint bookkeeping, and autoscaler configuration.
type KubeDetail struct {
	deps        Deps
	kubeContext string
	entry       kubeEntry
	load        func(string) ([]kube.FlinkDeployment, error)
	loadSession func(string) ([]kube.FlinkSessionJob, error)
	lastRun     time.Time
	vp          viewport.Model
	ready       bool
	err         string
}

// NewKubeDetail opens the detail for one row of the operator tree.
func NewKubeDetail(deps Deps, kubeContext string, e kubeEntry) *KubeDetail {
	return &KubeDetail{deps: deps, kubeContext: kubeContext, entry: e, lastRun: time.Now(),
		load: kube.Deployments, loadSession: kube.SessionJobs}
}

func (d *KubeDetail) Title() string   { return d.entry.name() }
func (d *KubeDetail) Filtering() bool { return false }
func (d *KubeDetail) Bindings() []key.Binding {
	bs := []key.Binding{binding("e", "e", "events")}
	if d.entry.deploy != nil {
		bs = append(bs, binding("p", "p", "pods"))
	}
	return append(bs,
		binding("y", "y", "yaml"),
		binding("up", "↑↓/pgup", "scroll"),
		binding("esc", "esc", "back"))
}

func (d *KubeDetail) Init() tea.Cmd { return nil }

type kubeDetailMsg struct {
	entry kubeEntry
	found bool
	err   error
}

// Refresh re-reads the resource, throttled like the list.
func (d *KubeDetail) Refresh() tea.Cmd {
	if time.Since(d.lastRun) < kubeRefreshEvery {
		return nil
	}
	d.lastRun = time.Now()
	kc, ns, name, isDeploy := d.kubeContext, d.entry.namespace(), d.entry.name(), d.entry.deploy != nil
	load, loadSession := d.load, d.loadSession
	return func() tea.Msg {
		if isDeploy {
			ds, err := load(kc)
			if err != nil {
				return kubeDetailMsg{err: err}
			}
			for i := range ds {
				if ds[i].Namespace == ns && ds[i].Name == name {
					return kubeDetailMsg{entry: kubeEntry{deploy: &ds[i]}, found: true}
				}
			}
			return kubeDetailMsg{}
		}
		ss, err := loadSession(kc)
		if err != nil {
			return kubeDetailMsg{err: err}
		}
		for i := range ss {
			if ss[i].Namespace == ns && ss[i].Name == name {
				return kubeDetailMsg{entry: kubeEntry{session: &ss[i]}, found: true}
			}
		}
		return kubeDetailMsg{}
	}
}

func (d *KubeDetail) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case kubeDetailMsg:
		switch {
		case msg.err != nil:
			d.err = msg.err.Error()
		case !msg.found:
			d.err = "resource no longer exists (deleted?)"
		default:
			d.err = ""
			parent := d.entry.parent
			d.entry = msg.entry
			d.entry.parent = parent
		}
		return d, nil
	case tea.KeyMsg:
		ns, name := d.entry.namespace(), d.entry.name()
		switch msg.String() {
		case "e":
			return d, push(NewKubeEvents(d.deps, d.kubeContext, ns, name))
		case "p":
			if d.entry.deploy != nil {
				return d, push(NewKubePods(d.deps, d.kubeContext, ns, name))
			}
		case "y":
			kind := "flinkdeployment"
			if d.entry.session != nil {
				kind = "flinksessionjob"
			}
			return d, push(NewTextPager(d.deps, name+".yaml", throttled(func() (string, error) {
				return kube.YAML(d.kubeContext, kind, ns, name)
			})))
		}
		if d.ready {
			var cmd tea.Cmd
			d.vp, cmd = d.vp.Update(msg)
			return d, cmd
		}
	}
	return d, nil
}

// throttled caches fetch's result for kubeRefreshEvery so a pager's
// per-tick Refresh doesn't exec kubectl every couple of seconds.
func throttled(fetch func() (string, error)) func() (string, error) {
	var at time.Time
	var text string
	var err error
	return func() (string, error) {
		if !at.IsZero() && time.Since(at) < kubeRefreshEvery {
			return text, err
		}
		text, err = fetch()
		at = time.Now()
		return text, err
	}
}

// ago renders a timestamp as a compact age ("-" when unknown).
func ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return model.FmtAge(t.UnixMilli())
}

func renderCell(c uitable.Cell) string { return c.Style.Render(c.Text) }

func kv(k, v string) string {
	return styles.Dim().Render(fmt.Sprintf("  %-18s", k)) + v + "\n"
}

func section(title string) string {
	return "\n" + styles.ViewTitle().Render(" "+title) + "\n"
}

// body renders the detail text (pure, so tests can assert on it).
func (d *KubeDetail) body() string {
	var b strings.Builder
	e := d.entry
	var job *kube.JobSpec
	var errFull, reconcileState string
	var reconciledAt time.Time
	var drifting, known bool
	var gen, observed int64

	b.WriteString(section("RESOURCE"))
	if dep := e.deploy; dep != nil {
		kind := "FlinkDeployment (application)"
		if dep.Session() {
			kind = "FlinkDeployment (session cluster)"
		}
		b.WriteString(kv("kind", kind))
		b.WriteString(kv("namespace", dep.Namespace))
		b.WriteString(kv("flink", orDash(dep.FlinkVersion)+"  mode "+orDash(dep.Mode)))
		b.WriteString(kv("lifecycle", renderCell(lifecycleStyle(dep.Lifecycle))))
		b.WriteString(kv("jobmanager", orDash(dep.JMStatus)))
		b.WriteString(kv("job", orDash(dep.JobState)+"  "+styles.Dim().Render(orDash(dep.JobID))))
		job, errFull = dep.Job, dep.ErrorFull
		reconcileState, reconciledAt = dep.ReconcileState, dep.ReconciledAt
		drifting, known = dep.Drift()
		gen, observed = dep.Generation, dep.ObservedGeneration
	} else if s := e.session; s != nil {
		b.WriteString(kv("kind", "FlinkSessionJob"))
		b.WriteString(kv("namespace", s.Namespace))
		parent := s.DeploymentName
		if e.parent == nil {
			parent += styles.WarnText().Render("  (not found in the deployment list)")
		}
		b.WriteString(kv("session cluster", orDash(parent)))
		b.WriteString(kv("lifecycle", renderCell(lifecycleStyle(s.Lifecycle))))
		b.WriteString(kv("job", orDash(s.JobState)+"  "+styles.Dim().Render(orDash(s.JobID))))
		job, errFull = s.Job, s.ErrorFull
		reconcileState, reconciledAt = s.ReconcileState, s.ReconciledAt
		drifting, known = s.Drift()
		gen, observed = s.Generation, s.ObservedGeneration
	}

	b.WriteString(section("RECONCILE"))
	b.WriteString(kv("state", orDash(reconcileState)+styles.Dim().Render("  last reconciled "+ago(reconciledAt)+" ago")))
	switch {
	case !known:
		b.WriteString(kv("drift", styles.Dim().Render("unknown — operator does not report observedGeneration")))
	case drifting:
		b.WriteString(kv("drift", styles.WarnText().Render(fmt.Sprintf("⟳ spec generation %d not yet reconciled (operator at %d)", gen, observed))))
	default:
		b.WriteString(kv("drift", styles.OKText().Render(fmt.Sprintf("in sync (generation %d)", gen))))
	}

	if lines := kube.ErrorLines(errFull); len(lines) > 0 {
		b.WriteString(section("ERROR"))
		for _, l := range lines {
			b.WriteString("  " + styles.ErrText().Render(l) + "\n")
		}
	}

	if job != nil {
		b.WriteString(section("JOB SPEC"))
		b.WriteString(kv("jar", orDash(job.JarURI)))
		if job.EntryClass != "" {
			b.WriteString(kv("entry class", job.EntryClass))
		}
		if len(job.Args) > 0 {
			b.WriteString(kv("args", strings.Join(job.Args, " ")))
		}
		b.WriteString(kv("parallelism", fmt.Sprint(job.Parallelism)))
		mode := orDash(job.UpgradeMode)
		if job.UpgradeMode == "stateless" {
			mode = styles.WarnText().Render(mode + " — upgrades and restarts drop all state")
		}
		b.WriteString(kv("upgrade mode", mode))
		b.WriteString(kv("desired state", orDash(job.State)))
		if job.InitialSavepointPath != "" {
			b.WriteString(kv("initial savepoint", job.InitialSavepointPath))
		}
	}

	if dep := e.deploy; dep != nil && !dep.Session() {
		b.WriteString(section("SAVEPOINTS (operator)"))
		if dep.UpgradeSavepoint != "" {
			b.WriteString(kv("upgrade savepoint", dep.UpgradeSavepoint))
		}
		if dep.LastSavepoint != nil {
			sp := dep.LastSavepoint
			b.WriteString(kv("last", sp.Location+styles.Dim().Render(fmt.Sprintf("  %s · %s ago", orDash(sp.TriggerType), ago(sp.Time)))))
		}
		for i, sp := range dep.SavepointHistory {
			if i >= 10 {
				b.WriteString(styles.Dim().Render(fmt.Sprintf("  … %d older", len(dep.SavepointHistory)-10)) + "\n")
				break
			}
			b.WriteString(styles.Dim().Render(fmt.Sprintf("  %8s ago  %-9s ", ago(sp.Time), orDash(sp.TriggerType))) + sp.Location + "\n")
		}
		if dep.LastSavepoint == nil && len(dep.SavepointHistory) == 0 && dep.UpgradeSavepoint == "" {
			b.WriteString(styles.Dim().Render("  none recorded in status (newer operators track them as FlinkStateSnapshot resources)") + "\n")
		}

		b.WriteString(section("AUTOSCALER"))
		switch AutoscalerMode(dep.FlinkConfig) {
		case "on":
			b.WriteString(kv("mode", styles.OKText().Render("● enabled — scaling decisions are applied")))
		case "advisory":
			b.WriteString(kv("mode", styles.WarnText().Render("◐ advisory — recommendations only (scaling.enabled=false)")))
		default:
			b.WriteString(kv("mode", styles.Dim().Render("○ off")))
		}
		for _, k := range autoscalerKeys(dep.FlinkConfig) {
			b.WriteString(kv(strings.TrimPrefix(strings.TrimPrefix(k, "kubernetes.operator."), "job.autoscaler."), dep.FlinkConfig[k]))
		}
	}
	return b.String()
}

// autoscalerKeys lists the CR's autoscaler settings, sorted.
func autoscalerKeys(conf map[string]string) []string {
	var ks []string
	for k := range conf {
		if strings.Contains(k, "job.autoscaler.") && !strings.HasSuffix(k, "autoscaler.enabled") {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	return ks
}

func (d *KubeDetail) View(width, height int) string {
	return framed(width, height, d.entry.name(), -1, "via kubectl · context "+d.kubeContext, func(iw, ih int) string {
		out := ""
		used := 0
		if d.err != "" {
			out += errorBanner(d.err)
			used++
		}
		if !d.ready {
			d.vp = viewport.New(iw, ih-used)
			d.ready = true
		}
		d.vp.Width, d.vp.Height = iw, max(1, ih-used)
		d.vp.SetContent(lipgloss.NewStyle().Width(iw).Render(d.body())) // long operator errors wrap
		return out + d.vp.View()
	})
}

type kubeEventsMsg struct {
	events []kube.Event
	err    error
}

// KubeEvents lists the Kubernetes events on one operator resource — where
// the operator reports upgrades, savepoint errors and autoscaler decisions.
type KubeEvents struct {
	deps        Deps
	kubeContext string
	ns, name    string
	load        func(kubeContext, ns, name string) ([]kube.Event, error)
	tbl         *uitable.Model
	events      []kube.Event
	lastRun     time.Time
	loaded      bool
	err         string
}

// NewKubeEvents builds the events view for one resource.
func NewKubeEvents(deps Deps, kubeContext, ns, name string) *KubeEvents {
	tbl := newTable([]uitable.Column{
		{Title: "TYPE", Width: 8},
		{Title: "REASON", Width: 22},
		{Title: "AGE", Width: 8, Right: true},
		{Title: "COUNT", Width: 5, Right: true},
		{Title: "MESSAGE", Width: -1},
	})
	return &KubeEvents{deps: deps, kubeContext: kubeContext, ns: ns, name: name, load: kube.Events, tbl: tbl}
}

func (k *KubeEvents) Title() string   { return "events" }
func (k *KubeEvents) Filtering() bool { return false }
func (k *KubeEvents) Bindings() []key.Binding {
	return []key.Binding{binding("enter", "enter", "full message"), binding("esc", "esc", "back")}
}

func (k *KubeEvents) Init() tea.Cmd {
	k.lastRun = time.Now()
	kc, ns, name, load := k.kubeContext, k.ns, k.name, k.load
	return func() tea.Msg {
		es, err := load(kc, ns, name)
		return kubeEventsMsg{events: es, err: err}
	}
}

func (k *KubeEvents) Refresh() tea.Cmd {
	if time.Since(k.lastRun) < kubeRefreshEvery {
		return nil
	}
	return k.Init()
}

func (k *KubeEvents) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case kubeEventsMsg:
		k.loaded = true
		if msg.err != nil {
			k.err = msg.err.Error()
			return k, nil
		}
		k.err = ""
		k.events = msg.events
		rows := make([][]uitable.Cell, 0, len(msg.events))
		for _, e := range msg.events {
			typ := uitable.Styled(e.Type, styles.Dim())
			if e.Type == "Warning" {
				typ = uitable.Styled(e.Type, styles.WarnText())
			}
			rows = append(rows, []uitable.Cell{
				typ,
				uitable.Plain(e.Reason),
				uitable.Plain(ago(e.Last)),
				uitable.Plain(fmt.Sprint(e.Count)),
				uitable.Plain(strings.ReplaceAll(e.Message, "\n", " ")),
			})
		}
		k.tbl.SetRows(rows)
		return k, nil
	case tea.KeyMsg:
		if msg.String() == "enter" {
			if cur := k.tbl.Cursor(); cur >= 0 && cur < len(k.events) {
				e := k.events[cur]
				text := fmt.Sprintf("%s  %s  ×%d  %s ago\n\n%s\n", e.Type, e.Reason, e.Count, ago(e.Last), e.Message)
				return k, push(NewTextPager(k.deps, e.Reason, func() (string, error) { return text, nil }))
			}
		}
		k.tbl.Update(msg)
	}
	return k, nil
}

func (k *KubeEvents) View(width, height int) string {
	return framed(width, height, "Events "+k.ns+"/"+k.name, k.tbl.Len(), "via kubectl · context "+k.kubeContext, func(iw, ih int) string {
		if k.err != "" {
			return errorBanner(k.err)
		}
		if !k.loaded {
			return emptyState(" loading events…")
		}
		if k.tbl.Len() == 0 {
			return emptyState(" No events (Kubernetes keeps them ~1h by default).")
		}
		k.tbl.SetSize(iw, ih)
		return k.tbl.View()
	})
}

type kubePodsMsg struct {
	pods []kube.Pod
	err  error
}

// KubePods lists a deployment's JobManager/TaskManager pods — where OOM
// kills and crash loops show up before Flink's REST API notices anything.
type KubePods struct {
	deps        Deps
	kubeContext string
	ns, name    string
	load        func(kubeContext, ns, name string) ([]kube.Pod, error)
	tbl         *uitable.Model
	lastRun     time.Time
	loaded      bool
	err         string
}

// NewKubePods builds the pods view for one FlinkDeployment.
func NewKubePods(deps Deps, kubeContext, ns, name string) *KubePods {
	tbl := newTable([]uitable.Column{
		{Title: "NAME", Width: -1},
		{Title: "COMPONENT", Width: 11},
		{Title: "PHASE", Width: 9},
		{Title: "READY", Width: 5},
		{Title: "RESTARTS", Width: 8, Right: true},
		{Title: "REASON", Width: 18},
		{Title: "NODE", Width: 16},
		{Title: "AGE", Width: 8, Right: true},
	})
	return &KubePods{deps: deps, kubeContext: kubeContext, ns: ns, name: name, load: kube.Pods, tbl: tbl}
}

func (k *KubePods) Title() string   { return "pods" }
func (k *KubePods) Filtering() bool { return false }
func (k *KubePods) Bindings() []key.Binding {
	return []key.Binding{binding("esc", "esc", "back")}
}

func (k *KubePods) Init() tea.Cmd {
	k.lastRun = time.Now()
	kc, ns, name, load := k.kubeContext, k.ns, k.name, k.load
	return func() tea.Msg {
		ps, err := load(kc, ns, name)
		return kubePodsMsg{pods: ps, err: err}
	}
}

func (k *KubePods) Refresh() tea.Cmd {
	if time.Since(k.lastRun) < kubeRefreshEvery {
		return nil
	}
	return k.Init()
}

func (k *KubePods) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case kubePodsMsg:
		k.loaded = true
		if msg.err != nil {
			k.err = msg.err.Error()
			return k, nil
		}
		k.err = ""
		rows := make([][]uitable.Cell, 0, len(msg.pods))
		for _, p := range msg.pods {
			ready := uitable.Styled("yes", styles.OKText())
			if !p.Ready {
				ready = uitable.Styled("no", styles.ErrText())
			}
			restarts := uitable.Plain(fmt.Sprint(p.Restarts))
			if p.Restarts > 0 {
				restarts = uitable.Styled(fmt.Sprint(p.Restarts), styles.WarnText())
			}
			reason := uitable.Styled(p.Reason, styles.Dim())
			if strings.Contains(p.Reason, "OOMKilled") || strings.Contains(p.Reason, "CrashLoop") || strings.Contains(p.Reason, "Error") {
				reason = uitable.Styled(p.Reason, styles.ErrText())
			}
			rows = append(rows, []uitable.Cell{
				uitable.Plain(p.Name),
				uitable.Styled(orDash(p.Component), styles.Dim()),
				uitable.Styled(p.Phase, styles.StateStyle(podState(p.Phase))),
				ready,
				restarts,
				reason,
				uitable.Styled(p.Node, styles.Dim()),
				uitable.Plain(ago(p.Started)),
			})
		}
		k.tbl.SetRows(rows)
		return k, nil
	case tea.KeyMsg:
		k.tbl.Update(msg)
	}
	return k, nil
}

func podState(phase string) string {
	switch phase {
	case "Running", "Succeeded":
		return "RUNNING"
	case "Failed", "Unknown":
		return "FAILED"
	default:
		return "RESTARTING"
	}
}

func (k *KubePods) View(width, height int) string {
	return framed(width, height, "Pods "+k.ns+"/"+k.name, k.tbl.Len(), "via kubectl · label app="+k.name, func(iw, ih int) string {
		if k.err != "" {
			return errorBanner(k.err)
		}
		if !k.loaded {
			return emptyState(" loading pods…")
		}
		if k.tbl.Len() == 0 {
			return emptyState(" No pods labelled app=" + k.name + " (suspended, or a standalone deployment with custom labels).")
		}
		k.tbl.SetSize(iw, ih)
		return k.tbl.View()
	})
}
