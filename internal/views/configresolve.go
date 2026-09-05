package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type cfgResolveMsg struct {
	cluster  map[string]string
	job      model.JobConfig
	vertices []model.Vertex
	err      error
}

// ConfigResolve shows Flink's config override chain for one job: for each
// setting that layers (cluster flink-conf → job → operator), the value at
// every layer, the effective value, and which layer won. It answers "why
// is parallelism 4 here?" — the question the Web UI can't.
type ConfigResolve struct {
	deps  Deps
	jid   string
	name  string
	msg   cfgResolveMsg
	vp    viewport.Model
	ready bool
}

// NewConfigResolve builds the resolution view for one job.
func NewConfigResolve(deps Deps, jid, name string) *ConfigResolve {
	return &ConfigResolve{deps: deps, jid: jid, name: name}
}

func (c *ConfigResolve) Title() string   { return "config" }
func (c *ConfigResolve) Filtering() bool { return false }
func (c *ConfigResolve) Bindings() []key.Binding {
	return []key.Binding{
		binding("up", "↑↓/pgup", "scroll"),
		binding("r", "r", "refresh"),
		binding("esc", "esc", "back"),
	}
}

func (c *ConfigResolve) Init() tea.Cmd { return c.Refresh() }

func (c *ConfigResolve) Refresh() tea.Cmd {
	cl := c.deps.CL
	jid := c.jid
	return func() tea.Msg {
		entries, err := cl.JobManagerConfig()
		if err != nil {
			return cfgResolveMsg{err: err}
		}
		cluster := make(map[string]string, len(entries))
		for _, e := range entries {
			cluster[e.Key] = e.Value
		}
		jc, err := cl.JobConfig(jid)
		if err != nil {
			return cfgResolveMsg{err: err}
		}
		job, err := cl.Job(jid)
		if err != nil {
			return cfgResolveMsg{err: err}
		}
		return cfgResolveMsg{cluster: cluster, job: jc, vertices: job.Vertices}
	}
}

func (c *ConfigResolve) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case cfgResolveMsg:
		c.msg = msg
		if c.ready {
			c.vp.SetContent(c.render())
		}
		return c, nil
	case tea.KeyMsg:
		if c.ready {
			var cmd tea.Cmd
			c.vp, cmd = c.vp.Update(msg)
			return c, cmd
		}
	}
	return c, nil
}

// layerRow is one setting's resolution across layers.
type layerRow struct {
	setting    string
	cluster    string // "" = layer sets nothing
	job        string
	eff        string
	source     string // cluster | job | operators
	overridden bool   // a lower layer beat a higher one
}

func firstNonEmpty(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

func (c *ConfigResolve) rows() []layerRow {
	cl := c.msg.cluster
	jc := c.msg.job.ExecutionConfig
	uc := func(k string) string {
		if v, ok := jc.UserConfig[k]; ok {
			return fmt.Sprint(v)
		}
		return ""
	}
	var rows []layerRow

	// parallelism: cluster default → job → (operators handled separately)
	{
		clv := firstNonEmpty(cl, "parallelism.default")
		jbv := fmt.Sprint(jc.JobParallelism)
		r := layerRow{setting: "parallelism", cluster: clv, job: jbv, eff: jbv, source: "job"}
		if clv == jbv || clv == "" {
			r.source = "cluster default"
			if clv == "" {
				r.source = "job"
			}
		} else {
			r.overridden = true
		}
		rows = append(rows, r)
	}
	// restart strategy: the job string tells us if it inherits
	{
		jbv := jc.RestartStrategy
		clv := firstNonEmpty(cl, "restart-strategy.type", "restart-strategy")
		r := layerRow{setting: "restart strategy", cluster: clv, job: jbv, eff: jbv, source: "job"}
		if strings.Contains(strings.ToLower(jbv), "cluster level default") {
			r.source = "cluster default"
			r.eff = clv
			if clv == "" {
				r.eff = "(cluster default)"
			}
		} else if clv != "" && clv != jbv {
			r.overridden = true
		}
		rows = append(rows, r)
	}
	// checkpoint interval
	rows = append(rows, resolveKey("checkpoint interval", cl, uc,
		[]string{"execution.checkpointing.interval"}, "execution.checkpointing.interval"))
	// state backend
	rows = append(rows, resolveKey("state backend", cl, uc,
		[]string{"state.backend.type", "state.backend"}, "state.backend.type"))
	// object reuse (job-level boolean, no cluster equivalent surfaced)
	rows = append(rows, layerRow{setting: "object reuse", cluster: firstNonEmpty(cl, "pipeline.object-reuse"),
		job: fmt.Sprint(jc.ObjectReuse), eff: fmt.Sprint(jc.ObjectReuse), source: "job"})

	// any other explicit job user-config overrides
	var extra []string
	for k := range jc.UserConfig {
		extra = append(extra, k)
	}
	sort.Strings(extra)
	for _, k := range extra {
		if k == "" {
			continue
		}
		rows = append(rows, resolveKey(k, cl, uc, []string{k}, k))
	}
	return rows
}

// resolveKey builds a row from a cluster key set and a job user-config key.
func resolveKey(setting string, cl map[string]string, uc func(string) string, clusterKeys []string, jobKey string) layerRow {
	clv := firstNonEmpty(cl, clusterKeys...)
	jbv := uc(jobKey)
	r := layerRow{setting: setting, cluster: clv, job: jbv}
	switch {
	case jbv != "" && jbv != clv:
		r.eff, r.source, r.overridden = jbv, "job override", clv != ""
	case clv != "":
		r.eff, r.source = clv, "cluster"
	default:
		r.eff, r.source = "(default)", "flink built-in"
	}
	return r
}

func (c *ConfigResolve) render() string {
	if c.msg.err != nil {
		return styles.ErrText().Render(" ⚠ " + c.msg.err.Error())
	}
	if c.msg.cluster == nil {
		return styles.Dim().Render(" resolving config layers…")
	}
	var b strings.Builder
	dim, na := styles.Dim(), styles.Dim().Render("—")
	b.WriteString(styles.Header().Render(fmt.Sprintf(" %-20s %-16s %-16s %s", "SETTING", "CLUSTER", "JOB", "EFFECTIVE")) + "\n")
	for _, r := range c.rows() {
		clv, jbv := na, na
		if r.cluster != "" {
			clv = dim.Render(clip1(r.cluster, 16))
		}
		if r.job != "" {
			jbv = dim.Render(clip1(r.job, 16))
		}
		effStyle := styles.OKText()
		mark := "  "
		if r.overridden {
			effStyle = styles.WarnText()
			mark = styles.WarnText().Render("↳ ")
		}
		eff := effStyle.Render(clip1(r.eff, 14)) + dim.Render("  "+r.source)
		b.WriteString(fmt.Sprintf("%s%-18s %-16s %-16s %s\n", mark, clip1(r.setting, 18), clv, jbv, eff))
	}

	// parallelism by operator
	b.WriteString("\n" + styles.Header().Render(fmt.Sprintf(" parallelism by operator (job default %d)", c.msg.job.ExecutionConfig.JobParallelism)) + "\n")
	jd := c.msg.job.ExecutionConfig.JobParallelism
	for i, v := range c.msg.vertices {
		note, st := dim.Render("= job default"), dim
		if v.Parallelism != jd {
			note, st = styles.WarnText().Render("↳ operator override"), styles.WarnText()
			_ = st
		}
		b.WriteString(fmt.Sprintf("  #%-2d %s  %s  %s\n", i+1, styles.OKText().Render(fmt.Sprintf("p=%-3d", v.Parallelism)), clip1(v.Name, 44), note))
	}
	b.WriteString("\n" + dim.Render(" ↳ marks where a lower layer overrides a higher one. Flink's REST\n   attributes overrides to cluster→job→operator; deeper provenance\n   (e.g. -D submit flags) isn't exposed and isn't guessed here."))
	return b.String()
}

func clip1(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (c *ConfigResolve) View(width, height int) string {
	head := styles.ViewTitle().Render("Config resolution · "+c.name) +
		styles.Dim().Render("  the override chain: cluster → job → operator") + "\n"
	if !c.ready {
		c.vp = viewport.New(width, height-1)
		c.ready = true
		c.vp.SetContent(c.render())
	}
	c.vp.Width = width
	c.vp.Height = height - 1
	return head + c.vp.View()
}
