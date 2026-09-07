package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type bufferMsg struct {
	sp  client.SubtaskPressure
	err error
}

// BufferCompare shows a vertex's per-subtask backpressure fingerprint — the
// busy/backpressured/idle trio plus the network buffer pools — so an operator
// can see the TRUE backpressure edge: a full OUTPUT pool means the downstream
// is the constraint (this vertex is a victim), a full INPUT pool means the
// upstream is starving it, and high busy with clear pools means this vertex is
// itself CPU-bound. The coarse per-vertex level can't make that distinction.
type BufferCompare struct {
	deps   Deps
	jid    string
	vid    string
	vertex string
	par    int
	sp     client.SubtaskPressure
	err    string
}

// NewBufferCompare builds the per-subtask buffer-pressure panel for one vertex.
func NewBufferCompare(deps Deps, jid, vid, vertex string, par int) *BufferCompare {
	return &BufferCompare{deps: deps, jid: jid, vid: vid, vertex: vertex, par: par}
}

func (b *BufferCompare) Title() string   { return "buffers" }
func (b *BufferCompare) Filtering() bool { return false }
func (b *BufferCompare) Bindings() []key.Binding {
	return []key.Binding{binding("esc", "esc", "back")}
}

func (b *BufferCompare) Init() tea.Cmd { return b.Refresh() }

func (b *BufferCompare) Refresh() tea.Cmd {
	cl, jid, vid, par := b.deps.CL, b.jid, b.vid, b.par
	return func() tea.Msg {
		sp, err := cl.VertexSubtaskPressure(jid, vid, par)
		return bufferMsg{sp: sp, err: err}
	}
}

func (b *BufferCompare) Update(msg tea.Msg) (Model, tea.Cmd) {
	if m, ok := msg.(bufferMsg); ok {
		if m.err != nil {
			b.err = m.err.Error()
			return b, nil
		}
		b.err, b.sp = "", m.sp
	}
	return b, nil
}

func (b *BufferCompare) View(width, height int) string {
	var sb strings.Builder
	sb.WriteString(styles.ViewTitle().Render("Backpressure edge · "+b.vertex) + "\n")
	if b.err != "" {
		return sb.String() + styles.ErrText().Render(" ⚠ "+b.err)
	}
	n := len(b.sp.Busy)
	if n == 0 {
		return sb.String() + styles.Dim().Render(" no per-subtask metrics yet")
	}

	// Verdict first — the edge classification is the point of this view.
	v := analyze.BackpressureEdge(b.sp.Busy, b.sp.InPool, b.sp.OutPool)
	vStyle := styles.Dim()
	switch v.Kind {
	case "downstream", "upstream":
		vStyle = styles.ErrText()
	case "cpu":
		vStyle = styles.WarnText()
	case "ok":
		vStyle = styles.OKText()
	}
	sb.WriteString(" " + vStyle.Render("edge: "+v.Kind) + styles.Dim().Render(" — "+v.Summary) + "\n\n")

	hot := map[int]bool{}
	for _, i := range v.HotSubtasks {
		hot[i] = true
	}

	// Per-subtask rows, worst-first by output-pool then busy so the implicated
	// subtasks sit at the top.
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, c int) bool {
		if b.sp.OutPool[idx[a]] != b.sp.OutPool[idx[c]] {
			return b.sp.OutPool[idx[a]] > b.sp.OutPool[idx[c]]
		}
		return b.sp.Busy[idx[a]] > b.sp.Busy[idx[c]]
	})

	sb.WriteString(styles.Dim().Render(fmt.Sprintf(" %-5s %-14s %6s %6s   %-10s %-10s", "ST", "busy", "bp", "idle", "inPool", "outPool")) + "\n")
	limit := n
	if height > 8 && limit > height-8 {
		limit = height - 8
	}
	for _, i := range idx[:limit] {
		mark := "  "
		if hot[i] {
			mark = styles.ErrText().Render("▸ ")
		}
		busyBar := styles.Gauge(b.sp.Busy[i]/1000, 10, 0.75, 0.90)
		inBar := styles.Gauge(b.sp.InPool[i], 8, 0.75, 0.90)
		outBar := styles.Gauge(b.sp.OutPool[i], 8, 0.75, 0.90)
		sb.WriteString(fmt.Sprintf(" %s%-3d %s %5.0f %6.0f %6.0f   %s %4.0f%% %s %4.0f%%\n",
			mark, i, busyBar, b.sp.Busy[i], b.sp.Backpressured[i], b.sp.Idle[i],
			inBar, b.sp.InPool[i]*100, outBar, b.sp.OutPool[i]*100))
	}
	if limit < n {
		sb.WriteString(styles.Dim().Render(fmt.Sprintf(" … %d more subtasks", n-limit)) + "\n")
	}
	return sb.String()
}
