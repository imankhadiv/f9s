package views

import (
	"fmt"
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
	return &KubeDeploys{deps: deps, tbl: tbl}
}

func (k *KubeDeploys) Title() string   { return "flinkdeployments" }
func (k *KubeDeploys) Filtering() bool { return false }
func (k *KubeDeploys) Bindings() []key.Binding {
	return []key.Binding{binding("esc", "esc", "back")}
}

func (k *KubeDeploys) Init() tea.Cmd {
	k.lastRun = time.Now()
	return func() tea.Msg {
		ds, err := kube.Deployments("")
		return kubeMsg{deploys: ds, err: err}
	}
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
	out := styles.ViewTitle().Render(fmt.Sprintf("FlinkDeployments[%d]", k.tbl.Len())) +
		styles.Dim().Render("  via kubectl · refreshes every 10s") + "\n"
	used := 1
	if k.err != "" {
		return out + styles.ErrText().Render(" ⚠ "+k.err)
	}
	if !k.loaded {
		return out + styles.Dim().Render(" running kubectl get flinkdeployments -A …")
	}
	if k.tbl.Len() == 0 {
		return out + styles.Dim().Render(" No FlinkDeployments found on the current kube context.")
	}
	k.tbl.SetSize(width, height-used)
	return out + k.tbl.View()
}
