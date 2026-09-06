package views

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/doctor"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type doctorMsg struct {
	findings []doctor.Finding
	err      error
}

// DoctorView is the in-TUI cluster lint: the same rules as `f9s doctor`.
type DoctorView struct {
	deps     Deps
	tbl      *uitable.Model
	findings []doctor.Finding
	err      string
}

// NewDoctor builds the lint view.
func NewDoctor(deps Deps) *DoctorView {
	tbl := newTable([]uitable.Column{
		{Title: "SEV", Width: 7},
		{Title: "AREA", Width: 40},
		{Title: "FINDING", Width: -1},
	})
	return &DoctorView{deps: deps, tbl: tbl}
}

func (d *DoctorView) Title() string   { return "doctor" }
func (d *DoctorView) Filtering() bool { return false }
func (d *DoctorView) Bindings() []key.Binding {
	return []key.Binding{binding("esc", "esc", "back")}
}

func (d *DoctorView) Init() tea.Cmd { return d.Refresh() }

func (d *DoctorView) Refresh() tea.Cmd {
	cl := d.deps.CL
	return func() tea.Msg {
		findings, err := doctor.Run(cl)
		return doctorMsg{findings: findings, err: err}
	}
}

func sevCell(s doctor.Severity) uitable.Cell {
	switch s {
	case doctor.SevCritical:
		return uitable.Styled("✗ CRIT", styles.ErrText())
	case doctor.SevWarn:
		return uitable.Styled("⚠ WARN", styles.WarnText())
	case doctor.SevInfo:
		return uitable.Styled("ℹ INFO", styles.Dim())
	}
	return uitable.Styled("✓ OK", styles.OKText())
}

func (d *DoctorView) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case doctorMsg:
		if msg.err != nil {
			d.err = msg.err.Error()
			return d, nil
		}
		d.err = ""
		d.findings = msg.findings
		rows := make([][]uitable.Cell, 0, len(msg.findings))
		keys := make([]string, 0, len(msg.findings))
		for _, f := range msg.findings {
			keys = append(keys, f.Area+f.Text)
			text := f.Text
			if f.Remedy != "" {
				text += "  → " + f.Remedy
			}
			rows = append(rows, []uitable.Cell{
				sevCell(f.Sev),
				uitable.Plain(f.Area),
				uitable.Plain(text),
			})
		}
		d.tbl.SetRowsKeyed(rows, keys)
		return d, nil
	case tea.KeyMsg:
		d.tbl.Update(msg)
	}
	return d, nil
}

func (d *DoctorView) View(width, height int) string {
	return framed(width, height, "Doctor", d.tbl.Len(), d.deps.Ctx+" · config smells before they page you · also: f9s doctor (CLI)", func(iw, ih int) string {
		out := ""
		used := 0
		if d.err != "" {
			out += errorBanner(d.err)
			used++
		}
		d.tbl.SetSize(iw, ih-used)
		return out + d.tbl.View()
	})
}
