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
	deploys []kube.FlinkDeployment
	err     error
}

// KubeDeploys lists Flink Kubernetes Operator FlinkDeployments via kubectl.
type KubeDeploys struct {
	deps    Deps
	tbl     *uitable.Model
	load    func(string) ([]kube.FlinkDeployment, error)
	lastRun time.Time
	loaded  bool
	err     string
}

// NewKubeDeploys builds the operator view.
func NewKubeDeploys(deps Deps) *KubeDeploys {
	tbl := newTable([]uitable.Column{
		{Title: "NAMESPACE", Width: 14},
		{Title: "NAME", Width: -1},
		{Title: "LIFECYCLE", Width: 11},
		{Title: "JM", Width: 9},
		{Title: "JOB STATE", Width: 12},
		{Title: "FLINK", Width: 6},
		{Title: "ERROR", Width: 30},
	})
	return &KubeDeploys{deps: deps, tbl: tbl, load: kube.Deployments}
}

func (k *KubeDeploys) Title() string   { return "flinkdeployments" }
func (k *KubeDeploys) Filtering() bool { return false }
func (k *KubeDeploys) Bindings() []key.Binding {
	return []key.Binding{binding("esc", "esc", "back")}
}

func (k *KubeDeploys) Init() tea.Cmd {
	k.lastRun = time.Now()
	kubeContext, err := k.configuredKubeContext()
	if err != nil {
		return func() tea.Msg { return kubeMsg{err: err} }
	}
	load := k.load
	return func() tea.Msg {
		ds, err := load(kubeContext)
		return kubeMsg{deploys: ds, err: err}
	}
}

func (k *KubeDeploys) configuredKubeContext() (string, error) {
	if k.deps.Cfg != nil {
		if ctx, ok := k.deps.Cfg.Contexts[k.deps.Ctx]; ok {
			if kubeContext := strings.TrimSpace(ctx.KubeContext); kubeContext != "" {
				return kubeContext, nil
			}
		}
	}
	return "", fmt.Errorf("Kubernetes access is not configured for f9s context %q; set kubeContext in config.yaml", k.deps.Ctx)
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

func (k *KubeDeploys) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case kubeMsg:
		k.loaded = true
		if msg.err != nil {
			k.err = msg.err.Error()
			return k, nil
		}
		k.err = ""
		rows := make([][]uitable.Cell, 0, len(msg.deploys))
		for _, d := range msg.deploys {
			rows = append(rows, []uitable.Cell{
				uitable.Styled(d.Namespace, styles.Dim()),
				uitable.Plain(d.Name),
				lifecycleStyle(d.Lifecycle),
				uitable.Styled(d.JMStatus, styles.StateStyle(mapJM(d.JMStatus))),
				uitable.Styled(d.JobState, styles.StateStyle(d.JobState)),
				uitable.Plain(d.FlinkVersion),
				uitable.Styled(d.Error, styles.ErrText()),
			})
		}
		k.tbl.SetRows(rows)
		return k, nil
	case tea.KeyMsg:
		k.tbl.Update(msg)
	}
	return k, nil
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
		k.tbl.SetSize(iw, ih-used)
		return out + k.tbl.View()
	})
}
