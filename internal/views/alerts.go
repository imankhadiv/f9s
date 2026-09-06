package views

import (
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/sentinel"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

// AlertsView lists firing alerts and past episodes from the sentinel.
type AlertsView struct {
	deps   Deps
	tbl    *uitable.Model
	filter viewFilter
}

// NewAlerts builds the alerts view.
func NewAlerts(deps Deps) *AlertsView {
	tbl := newTable([]uitable.Column{
		{Title: "STATE", Width: 9},
		{Title: "KIND", Width: 12},
		{Title: "JOB", Width: 22},
		{Title: "VERTEX", Width: -1},
		{Title: "OPENED", Width: 8},
		{Title: "DURATION", Width: 9, Right: true},
		{Title: "PEAK", Width: 5, Right: true},
		{Title: "AT FIRE", Width: 10},
	})
	return &AlertsView{deps: deps, tbl: tbl, filter: newViewFilter()}
}

func (a *AlertsView) Title() string   { return "alerts" }
func (a *AlertsView) Filtering() bool { return a.filter.Active() }
func (a *AlertsView) Bindings() []key.Binding {
	return []key.Binding{binding("/", "/", "filter"), binding("esc", "esc", "back")}
}

func (a *AlertsView) Init() tea.Cmd { return a.Refresh() }

type alertsRefreshMsg struct{}

func (a *AlertsView) Refresh() tea.Cmd {
	return func() tea.Msg { return alertsRefreshMsg{} }
}

func (a *AlertsView) rebuild() {
	sent := a.deps.Sentinel
	if sent == nil {
		return
	}
	rows := make([][]uitable.Cell, 0)
	for _, al := range sent.OpenAlerts() {
		rows = append(rows, alertRow(al))
	}
	for _, al := range sent.Episodes() {
		rows = append(rows, alertRow(al))
	}
	a.tbl.SetRows(rows)
}

func alertRow(al sentinel.Alert) []uitable.Cell {
	state := uitable.Styled("FIRING", styles.ErrText())
	dur := time.Since(al.OpenedAt)
	if !al.Open() {
		state = uitable.Styled("resolved", styles.Dim())
		dur = al.ClosedAt.Sub(al.OpenedAt)
	}
	return []uitable.Cell{
		state,
		uitable.Plain(al.Kind),
		uitable.Plain(al.Job),
		uitable.Plain(al.Vertex),
		uitable.Styled(al.OpenedAt.Format("15:04:05"), styles.Dim()),
		uitable.Plain(model.FmtDuration(dur.Milliseconds())),
		uitable.Styled(al.Peak, styles.StateStyle("FAILED")),
		uitable.Styled(atFireSpark(al.AtFire), styles.WarnText()),
	}
}

// atFireSpark renders the frozen at-fire distribution (skew alerts) as a
// sorted mini-skyline; empty for alert kinds without one.
func atFireSpark(values []float64) string {
	if len(values) < 2 {
		return ""
	}
	sky := analyze.Skyline(values, 8)
	max := sky[0]
	if max <= 0 {
		return ""
	}
	runes := []rune("▁▂▃▄▅▆▇█")
	var out []rune
	for _, v := range sky {
		idx := int(v / max * float64(len(runes)-1))
		if idx >= len(runes) {
			idx = len(runes) - 1
		}
		out = append(out, runes[idx])
	}
	return string(out)
}

func (a *AlertsView) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg.(type) {
	case alertsRefreshMsg:
		a.rebuild()
		return a, nil
	case tea.KeyMsg:
		if handled, cmd := a.filter.Handle(msg.(tea.KeyMsg), a.tbl); handled {
			return a, cmd
		}
		a.tbl.Update(msg.(tea.KeyMsg))
	}
	return a, nil
}

func (a *AlertsView) View(width, height int) string {
	a.rebuild()
	openN := 0
	if a.deps.Sentinel != nil {
		openN = len(a.deps.Sentinel.OpenAlerts())
	}
	return framed(width, height, "Alerts", a.tbl.Len(), "firing: "+strconv.Itoa(openN)+" · fire/clear after 3 samples", func(iw, ih int) string {
		out := ""
		used := 0
		if fl := a.filter.Line(); fl != "" {
			out += fl
			used++
		}
		if a.tbl.Len() == 0 && a.filter.input.Value() == "" {
			return out + emptyState(" No alerts. The sentinel samples backpressure every 15s on running jobs.")
		}
		a.tbl.SetSize(iw, ih-used)
		return out + a.tbl.View()
	})
}
