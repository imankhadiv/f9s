package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/sentinel"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// spark renders values (0..max) as a unicode sparkline of the last width
// samples.
func spark(values []float64, max float64, width int) string {
	if len(values) > width {
		values = values[len(values)-width:]
	}
	if max <= 0 {
		max = 1
	}
	var b strings.Builder
	for _, v := range values {
		if v < 0 {
			b.WriteRune('·')
			continue
		}
		idx := int(v / max * float64(len(sparkRunes)-1))
		if idx >= len(sparkRunes) {
			idx = len(sparkRunes) - 1
		}
		b.WriteRune(sparkRunes[idx])
	}
	return b.String()
}

// BPHistory shows a job's sentinel history: per-vertex busy sparklines with
// backpressured samples highlighted, plus this job's alert episodes.
type BPHistory struct {
	deps     Deps
	jid      string
	name     string
	vertices []model.Vertex
}

// NewBPHistory builds the history view for one job.
func NewBPHistory(deps Deps, jid, name string, vertices []model.Vertex) *BPHistory {
	return &BPHistory{deps: deps, jid: jid, name: name, vertices: vertices}
}

func (h *BPHistory) Title() string   { return "bp history" }
func (h *BPHistory) Filtering() bool { return false }
func (h *BPHistory) Bindings() []key.Binding {
	return []key.Binding{binding("esc", "esc", "back")}
}

func (h *BPHistory) Init() tea.Cmd    { return nil }
func (h *BPHistory) Refresh() tea.Cmd { return nil } // sentinel fills history in the background

func (h *BPHistory) Update(msg tea.Msg) (Model, tea.Cmd) { return h, nil }

func (h *BPHistory) View(width, height int) string {
	sent := h.deps.Sentinel
	var b strings.Builder
	b.WriteString(styles.ViewTitle().Render("Backpressure history · "+h.name) +
		styles.Dim().Render("  busy% per vertex · one cell = 15s · ! = backpressured sample") + "\n\n")
	sparkW := width - 44
	if sparkW < 20 {
		sparkW = 20
	}
	for _, v := range h.vertices {
		samples := sent.History(sentinel.Key{JID: h.jid, VID: v.ID})
		busy := make([]float64, len(samples))
		marks := 0
		for i, s := range samples {
			busy[i] = s.BusyMs
			if s.Level == "high" || s.Level == "low" {
				marks++
			}
		}
		name := v.Name
		if len(name) > 38 {
			name = name[:37] + "…"
		}
		line := fmt.Sprintf(" %-38s ", name)
		if len(samples) == 0 {
			b.WriteString(line + styles.Dim().Render("no samples yet — the sentinel ticks every 15s") + "\n")
			continue
		}
		sl := spark(busy, 1000, sparkW)
		style := styles.OKText()
		if marks > 0 {
			style = styles.WarnText()
		}
		b.WriteString(line + style.Render(sl) +
			styles.Dim().Render(fmt.Sprintf(" %d samples, %d backpressured", len(samples), marks)) + "\n")
	}
	b.WriteString("\n" + styles.ViewTitle().Render("Episodes") + "\n")
	n := 0
	for _, al := range append(sent.OpenAlerts(), sent.Episodes()...) {
		if al.Key.JID != h.jid {
			continue
		}
		n++
		state := styles.ErrText().Render("FIRING  ")
		dur := "ongoing"
		if !al.Open() {
			state = styles.Dim().Render("resolved")
			dur = model.FmtDuration(al.ClosedAt.Sub(al.OpenedAt).Milliseconds())
		}
		b.WriteString(fmt.Sprintf(" %s %s  %s  %s\n", state, al.OpenedAt.Format("15:04:05"), al.Vertex, dur))
	}
	if n == 0 {
		b.WriteString(styles.Dim().Render(" none for this job\n"))
	}
	return b.String()
}
