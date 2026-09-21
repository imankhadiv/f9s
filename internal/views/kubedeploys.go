package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/kube"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

// kubectl execs are heavier than REST calls; refresh at most this often.
const kubeRefreshEvery = 10 * time.Second

type kubeMsg struct {
	deploys  []kube.FlinkDeployment
	sessions []kube.FlinkSessionJob
	err      error
}

// kubeEntry is one row of the operator tree: a FlinkDeployment, or a
// FlinkSessionJob nested under its session cluster.
type kubeEntry struct {
	deploy  *kube.FlinkDeployment
	session *kube.FlinkSessionJob
	parent  *kube.FlinkDeployment // the session cluster a session job runs in (nil = not found)
}

func (e kubeEntry) namespace() string {
	if e.deploy != nil {
		return e.deploy.Namespace
	}
	return e.session.Namespace
}

func (e kubeEntry) name() string {
	if e.deploy != nil {
		return e.deploy.Name
	}
	return e.session.Name
}

// KubeDeploys lists Flink Kubernetes Operator resources via kubectl:
// FlinkDeployments, with FlinkSessionJobs nested under their session cluster.
type KubeDeploys struct {
	deps        Deps
	tbl         *uitable.Model
	load        func(string) ([]kube.FlinkDeployment, error)
	loadSession func(string) ([]kube.FlinkSessionJob, error)
	entries     []kubeEntry
	lastRun     time.Time
	loaded      bool
	err         string
}

// NewKubeDeploys builds the operator view.
func NewKubeDeploys(deps Deps) *KubeDeploys {
	tbl := newTable([]uitable.Column{
		{Title: "NAMESPACE", Width: 14},
		{Title: "NAME", Width: -1},
		{Title: "KIND", Width: 7},
		{Title: "LIFECYCLE", Width: 11},
		{Title: "JM", Width: 9},
		{Title: "JOB STATE", Width: 12},
		{Title: "RECONCILE", Width: 12},
		{Title: "AUTO", Width: 4},
		{Title: "FLINK", Width: 6},
		{Title: "ERROR", Width: 30},
	})
	return &KubeDeploys{deps: deps, tbl: tbl, load: kube.Deployments, loadSession: kube.SessionJobs}
}

func (k *KubeDeploys) Title() string   { return "flinkdeployments" }
func (k *KubeDeploys) Filtering() bool { return false }
func (k *KubeDeploys) Bindings() []key.Binding {
	return []key.Binding{
		binding("enter", "enter", "details"),
		binding("e", "e", "events"),
		binding("esc", "esc", "back"),
	}
}

func (k *KubeDeploys) Init() tea.Cmd {
	k.lastRun = time.Now()
	kubeContext, err := k.configuredKubeContext()
	if err != nil {
		return func() tea.Msg { return kubeMsg{err: err} }
	}
	load, loadSession := k.load, k.loadSession
	return func() tea.Msg {
		ds, err := load(kubeContext)
		if err != nil {
			return kubeMsg{err: err}
		}
		// Session jobs are optional: older operators lack the CRD, and a
		// cluster without it still has a perfectly good deployment list.
		var ss []kube.FlinkSessionJob
		if loadSession != nil {
			ss, _ = loadSession(kubeContext)
		}
		return kubeMsg{deploys: ds, sessions: ss}
	}
}

func (k *KubeDeploys) configuredKubeContext() (string, error) {
	return configuredKubeContext(k.deps)
}

// configuredKubeContext resolves the kubeconfig context mapped to the active
// f9s context. There is deliberately no fallback to kubectl's current context.
func configuredKubeContext(deps Deps) (string, error) {
	if deps.Cfg != nil {
		if ctx, ok := deps.Cfg.Contexts[deps.Ctx]; ok {
			if kubeContext := strings.TrimSpace(ctx.KubeContext); kubeContext != "" {
				return kubeContext, nil
			}
		}
	}
	return "", fmt.Errorf("Kubernetes access is not configured for f9s context %q; set kubeContext in config.yaml", deps.Ctx)
}

func (k *KubeDeploys) contextLabel() string {
	kubeContext, err := k.configuredKubeContext()
	if err != nil {
		return "context not configured"
	}
	return "context " + kubeContext
}

// Refresh throttles kubectl to once per kubeRefreshEvery.
func (k *KubeDeploys) Refresh() tea.Cmd {
	if time.Since(k.lastRun) < kubeRefreshEvery {
		return nil
	}
	return k.Init()
}

func lifecycleStyle(state string) uitable.Cell {
	switch state {
	case "STABLE", "DEPLOYED":
		return uitable.Styled(state, styles.OKText())
	case "FAILED", "ROLLED_BACK":
		return uitable.Styled(state, styles.ErrText())
	case "":
		return uitable.Styled("-", styles.Dim())
	default: // UPGRADING, SUSPENDED, ROLLING_BACK, ...
		return uitable.Styled(state, styles.WarnText())
	}
}

// reconcileCell shows the operator's reconcile state, with ⟳ when the spec
// was edited but not yet reconciled (generation > observedGeneration).
func reconcileCell(state string, drifting, known bool) uitable.Cell {
	if state == "" {
		state = "-"
	}
	switch {
	case known && drifting:
		return uitable.Styled(state+" ⟳", styles.WarnText())
	case state == "DEPLOYED":
		return uitable.Styled(state, styles.Dim())
	case state == "ROLLED_BACK":
		return uitable.Styled(state, styles.ErrText())
	case state == "-":
		return uitable.Styled(state, styles.Dim())
	default: // UPGRADING, ROLLING_BACK
		return uitable.Styled(state, styles.WarnText())
	}
}

// AutoscalerMode reads the operator autoscaler switches from the CR's
// flinkConfiguration: "on" (scaling applied), "advisory" (metrics collected
// and recommendations computed, scaling.enabled=false), or "off". Keys
// resolve like Flink's config: job.autoscaler.* wins, the legacy
// kubernetes.operator.job.autoscaler.* prefix is only a fallback.
func AutoscalerMode(conf map[string]string) string {
	if !strings.EqualFold(autoscalerFlag(conf, "enabled"), "true") {
		return "off"
	}
	if strings.EqualFold(autoscalerFlag(conf, "scaling.enabled"), "false") {
		return "advisory"
	}
	return "on"
}

func autoscalerFlag(conf map[string]string, suffix string) string {
	if v, ok := conf["job.autoscaler."+suffix]; ok {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(conf["kubernetes.operator.job.autoscaler."+suffix])
}

func autoCell(conf map[string]string, session bool) uitable.Cell {
	if session {
		return uitable.Styled("-", styles.Dim()) // the autoscaler scales application jobs
	}
	switch AutoscalerMode(conf) {
	case "on":
		return uitable.Styled("●", styles.OKText())
	case "advisory":
		return uitable.Styled("◐", styles.WarnText())
	}
	return uitable.Styled("○", styles.Dim())
}

// buildEntries orders the tree: each deployment, then the session jobs whose
// spec.deploymentName points at it; session jobs with no parent go last.
func buildEntries(ds []kube.FlinkDeployment, ss []kube.FlinkSessionJob) []kubeEntry {
	out := make([]kubeEntry, 0, len(ds)+len(ss))
	placed := make([]bool, len(ss))
	for i := range ds {
		d := &ds[i]
		out = append(out, kubeEntry{deploy: d})
		for j := range ss {
			if !placed[j] && ss[j].Namespace == d.Namespace && ss[j].DeploymentName == d.Name {
				out = append(out, kubeEntry{session: &ss[j], parent: d})
				placed[j] = true
			}
		}
	}
	for j := range ss {
		if !placed[j] {
			out = append(out, kubeEntry{session: &ss[j]})
		}
	}
	return out
}

func (k *KubeDeploys) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case kubeMsg:
		k.loaded = true
		if msg.err != nil {
			k.err = msg.err.Error()
			return k, nil
		}
		k.err = ""
		k.entries = buildEntries(msg.deploys, msg.sessions)
		rows := make([][]uitable.Cell, 0, len(k.entries))
		for _, e := range k.entries {
			rows = append(rows, entryRow(e))
		}
		k.tbl.SetRows(rows)
		return k, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "enter", "e":
			cur := k.tbl.Cursor()
			if cur < 0 || cur >= len(k.entries) {
				return k, nil
			}
			kubeContext, err := k.configuredKubeContext()
			if err != nil {
				return k, nil
			}
			e := k.entries[cur]
			if msg.String() == "e" {
				return k, push(NewKubeEvents(k.deps, kubeContext, e.namespace(), e.name()))
			}
			return k, push(NewKubeDetail(k.deps, kubeContext, e))
		}
		k.tbl.Update(msg)
	}
	return k, nil
}

func entryRow(e kubeEntry) []uitable.Cell {
	if d := e.deploy; d != nil {
		kind := "APP"
		if d.Session() {
			kind = "SESSION"
		}
		drift, known := d.Drift()
		return []uitable.Cell{
			uitable.Styled(d.Namespace, styles.Dim()),
			uitable.Plain(d.Name),
			uitable.Styled(kind, styles.Dim()),
			lifecycleStyle(d.Lifecycle),
			uitable.Styled(d.JMStatus, styles.StateStyle(mapJM(d.JMStatus))),
			uitable.Styled(d.JobState, styles.StateStyle(d.JobState)),
			reconcileCell(d.ReconcileState, drift, known),
			autoCell(d.FlinkConfig, d.Session()),
			uitable.Plain(d.FlinkVersion),
			uitable.Styled(d.Error, styles.ErrText()),
		}
	}
	s := e.session
	name := "  └ " + s.Name
	if e.parent == nil {
		name = s.Name + styles.Dim().Render(" (session "+orDash(s.DeploymentName)+" not found)")
	}
	drift, known := s.Drift()
	return []uitable.Cell{
		uitable.Styled(s.Namespace, styles.Dim()),
		uitable.Plain(name),
		uitable.Styled("JOB", styles.Dim()),
		lifecycleStyle(s.Lifecycle),
		uitable.Styled("", styles.Dim()),
		uitable.Styled(s.JobState, styles.StateStyle(s.JobState)),
		reconcileCell(s.ReconcileState, drift, known),
		uitable.Styled("-", styles.Dim()),
		uitable.Plain(""),
		uitable.Styled(s.Error, styles.ErrText()),
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// mapJM translates operator JM states into the shared state vocabulary.
func mapJM(s string) string {
	switch s {
	case "READY":
		return "RUNNING"
	case "MISSING", "ERROR":
		return "FAILED"
	case "DEPLOYING":
		return "RESTARTING"
	default:
		return s
	}
}

func (k *KubeDeploys) View(width, height int) string {
	kubeContext, ctxErr := k.configuredKubeContext()
	footer := "via kubectl · " + k.contextLabel() + " · refreshes every 10s"
	return framed(width, height, "FlinkDeployments", k.tbl.Len(), footer, func(iw, ih int) string {
		out := ""
		used := 0
		if k.err != "" {
			return out + errorBanner(k.err)
		}
		// Init reports this too, but its message lands a frame later — without
		// this the first frame advertises "kubectl --context  get …", an empty
		// context this view exists to refuse.
		if ctxErr != nil {
			return out + errorBanner(ctxErr.Error())
		}
		if !k.loaded {
			return out + emptyState(" running kubectl --context "+kubeContext+" get flinkdeployments -A …")
		}
		if k.tbl.Len() == 0 {
			return out + emptyState(" No FlinkDeployments found on Kubernetes context "+kubeContext+".")
		}
		out += styles.Dim().Render(" AUTO: ● autoscaler scaling · ◐ recommendations only · ○ off   RECONCILE ⟳ = spec edited, not yet reconciled") + "\n"
		used++
		k.tbl.SetSize(iw, ih-used)
		return out + k.tbl.View()
	})
}
