package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
)

// The f9s wordmark is drawn per-glyph (figlet "standard" font, k9s's line-art
// style) so each letter can take its own Flink-gradient color. Raw literals
// keep backslashes intact; rows are padded to equal width at render time, so
// trailing spaces don't matter. 's' is bottom-aligned with a blank top row.
var (
	logoF = []string{
		`  __`,
		` / _|`,
		`| |_`,
		`|  _|`,
		`|_|`,
	}
	logo9 = []string{
		`  ___`,
		` / _ \`,
		`| (_) |`,
		` \__, |`,
		`   /_/`,
	}
	logoS = []string{
		``,
		` ___`,
		`/ __|`,
		`\__ \`,
		`|___/`,
	}
)

// Flink-brand gradient for the three glyphs (purple → teal → orange, sweeping
// the Flink squirrel's gradient with three maximally-separated hues). Vivid on
// dark; the light values stay dark enough to read on a near-white terminal.
var (
	logoPurple lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#B491E8"}
	logoTeal   lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#3DD6C4"}
	logoOrange lipgloss.TerminalColor = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#F5A623"}
)

// logoTagline sits under the wordmark. Short so it fits beside the logo.
const logoTagline = "AI Flink TUI"

const logoRows = 6

// headerFull reports whether the terminal is big enough for the tall k9s-style
// header (keys + status + logo). Below this we fall back to the compact
// single-line header plus the footer legend.
func (a *App) headerFull() bool { return a.width >= 92 && a.height >= 20 }

// connState returns the rendered connection string, whether the cluster is
// healthy (logo shows its Flink gradient), and the fallback style the logo
// takes when it isn't (k9s turns its logo red on disconnect — a health signal).
func (a *App) connState() (conn string, healthy bool, down lipgloss.Style) {
	if !a.connected {
		if a.everSeen {
			return styles.WarnText().Render(fmt.Sprintf("DEGRADED %ds", int(time.Since(a.lastOK).Seconds()))),
				false, lipgloss.NewStyle().Foreground(styles.AmberColor()).Bold(true)
		}
		return styles.ErrText().Render("DOWN"), false, lipgloss.NewStyle().Foreground(styles.RedColor()).Bold(true)
	}
	conn = styles.OKText().Render("CONNECTED") + styles.Dim().Render(fmt.Sprintf(" ⟳%ds", int(time.Since(a.lastOK).Seconds())))
	return conn, true, lipgloss.Style{}
}

// header renders the top chrome: the tall three-region band when there's room,
// else the compact single-line bar.
func (a *App) header() string {
	conn, healthy, down := a.connState()
	if !a.headerFull() {
		return a.compactHeader(conn)
	}
	inner := a.width - 4 // PanelBox border(2) + padding(2)

	cols := 1
	if a.width >= 104 {
		cols = 2
	}
	left := a.keyGrid(a.top().Bindings(), globalBindings(), cols)
	mid := a.statusRows(conn)

	logo := renderLogo(healthy, down)
	logoW := lipgloss.Width(logo)
	const gap = 3
	leftW, midW := lipgloss.Width(left), lipgloss.Width(mid)

	blocks := []string{left, spacerBlock(gap), mid}
	if rem := inner - leftW - gap - midW; rem >= logoW+2 {
		blocks = append(blocks, spacerBlock(rem-logoW), logo)
	}
	content := lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
	content = lipgloss.NewStyle().Height(logoRows).MaxWidth(inner).Render(content)
	return styles.PanelBox().Width(a.width - 2).Render(content)
}

// spacerBlock is a blank column of the fixed header height, so a horizontal
// gap spans all rows rather than only the first.
func spacerBlock(w int) string {
	return lipgloss.NewStyle().Width(w).Height(logoRows).Render("")
}

// renderLogo draws the f9s wordmark plus its tagline. When healthy each glyph
// takes its Flink-gradient color; when the cluster is unreachable the whole
// mark collapses to a single state color (the logo doubles as a health light).
func renderLogo(healthy bool, down lipgloss.Style) string {
	glyph := func(block []string, c lipgloss.TerminalColor) string {
		st := lipgloss.NewStyle().Bold(true).Foreground(c)
		if !healthy {
			st = down
		}
		w := 0
		for _, r := range block {
			if n := lipgloss.Width(r); n > w {
				w = n
			}
		}
		rows := make([]string, len(block))
		for i, r := range block {
			rows[i] = st.Render(r + strings.Repeat(" ", w-lipgloss.Width(r)))
		}
		return lipgloss.JoinVertical(lipgloss.Left, rows...)
	}
	// One space between glyphs keeps the letters from touching.
	sp := lipgloss.NewStyle().Width(1).Height(len(logoF)).Render("")
	mark := lipgloss.JoinHorizontal(lipgloss.Top,
		glyph(logoF, logoPurple), sp, glyph(logo9, logoTeal), sp, glyph(logoS, logoOrange))

	tag := styles.Dim().Render(logoTagline)
	return lipgloss.JoinVertical(lipgloss.Right, mark, tag)
}

// statusRows renders the cluster metadata as aligned label:value rows.
// mode returns the effective deployment mode: the confident config-derived
// result when we have one, else a shape heuristic (an application cluster never
// legitimately hosts ≥2 jobs). The heuristic is marked NOT Confident, so it can
// inform the badge but never unlock the reassuring destructive-action copy.
func (a *App) mode() model.ModeInfo {
	if a.modeInfo.Confident {
		return a.modeInfo
	}
	total := a.ov.JobsRunning + a.ov.JobsFinished + a.ov.JobsCancelled + a.ov.JobsFailed
	if total >= 2 {
		return model.ModeInfo{Mode: model.ModeSession, Source: "heuristic"}
	}
	return a.modeInfo
}

// modeBadge renders the effective mode as a colored chip, or "" when undetected.
// A heuristic (unconfident) guess is dimmed and marked with "?" so it never
// reads as authoritatively as a config-confirmed mode. Folded onto the Context
// row so the header stays at its fixed 6-row height.
func (a *App) modeBadge() string {
	mi := a.mode()
	name := map[model.DeploymentMode]string{
		model.ModeApplication: "APP", model.ModeSession: "SESSION", model.ModeStandalone: "standalone",
	}[mi.Mode]
	if name == "" {
		return ""
	}
	if !mi.Confident {
		return styles.Dim().Render("● " + name + "?")
	}
	switch mi.Mode {
	case model.ModeApplication:
		return styles.Accent().Render("● " + name)
	case model.ModeStandalone:
		return styles.Dim().Render("○ " + name)
	default:
		return styles.OKText().Render("● " + name)
	}
}

func (a *App) statusRows(conn string) string {
	label := func(s string) string { return styles.Dim().Render(fmt.Sprintf("%-9s", s)) }

	ctxVal := styles.Header().Render(a.contextName)
	if b := a.modeBadge(); b != "" {
		ctxVal += "  " + b
	}
	if a.cfg.Contexts[a.contextName].ReadOnly {
		ctxVal += " " + styles.WarnText().Render("[RO]")
	}
	status := conn
	if a.fetching {
		status += " " + styles.Dim().Render(a.spin.View())
	}
	if n := len(a.sent.OpenAlerts()); n > 0 {
		status += "  " + styles.ErrText().Render(fmt.Sprintf("⚠%d", n))
	}
	jobs := fmt.Sprintf("%d", a.ov.JobsRunning)
	if a.mode().Mode == model.ModeSession {
		jobs += styles.Dim().Render(" (shared)")
	}
	rows := []string{
		label("Context:") + ctxVal,
		label("Flink:") + orDash(a.ov.FlinkVersion),
		label("Slots:") + fmt.Sprintf("%d/%d", a.ov.SlotsAvailable, a.ov.SlotsTotal),
		label("TMs:") + fmt.Sprintf("%d", a.ov.TaskManagers),
		label("Jobs:") + jobs,
		label("Status:") + status,
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// keyGrid lays the view's shortcuts (then the globals) out as k9s-style
// `<key> desc` cells, filled column-major into up to `cols` columns × 6 rows.
func (a *App) keyGrid(view, global []key.Binding, cols int) string {
	type cell struct{ key, desc string }
	collect := func(bs []key.Binding) []cell {
		var cs []cell
		for _, b := range bs {
			if h := b.Help(); h.Key != "" {
				cs = append(cs, cell{h.Key, h.Desc})
			}
		}
		return cs
	}
	vcells, gcells := collect(view), collect(global)

	// Reserve room for the globals so they're always visible; a busy view's
	// overflow collapses into a "<?> more" cell (the ? overlay lists them all).
	maxCells := logoRows * cols
	vroom := maxCells - len(gcells)
	if vroom < 0 {
		vroom = 0
	}
	cells := vcells
	if len(cells) > vroom {
		cells = append(append([]cell{}, vcells[:max(0, vroom-1)]...), cell{"?", "more"})
	}
	cells = append(cells, gcells...)
	if len(cells) > maxCells {
		cells = cells[:maxCells]
	}

	keyW := 0
	for _, c := range cells {
		if w := len([]rune(c.key)) + 2; w > keyW { // +2 for the <>
			keyW = w
		}
	}
	const descW = 12
	renderCell := func(c cell) string {
		k := styles.Accent().Bold(true).Render("<" + c.key + ">")
		k += strings.Repeat(" ", max(0, keyW-(len([]rune(c.key))+2)))
		return k + " " + styles.Dim().Render(truncStr(c.desc, descW))
	}

	colStrs := make([][]string, cols)
	for i, c := range cells {
		if col := i / logoRows; col < cols {
			colStrs[col] = append(colStrs[col], renderCell(c))
		}
	}
	cellW := keyW + 1 + descW
	var blocks []string
	for ci := 0; ci < cols; ci++ {
		for len(colStrs[ci]) < logoRows {
			colStrs[ci] = append(colStrs[ci], "")
		}
		if ci > 0 {
			blocks = append(blocks, spacerBlock(2))
		}
		blocks = append(blocks, lipgloss.NewStyle().Width(cellW).Height(logoRows).
			Render(lipgloss.JoinVertical(lipgloss.Left, colStrs[ci]...)))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, blocks...)
}

// truncStr clips to n runes with an ellipsis, rune-safe.
func truncStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// compactHeader is the original single-line bar, used on small terminals.
func (a *App) compactHeader(conn string) string {
	badge := ""
	if n := len(a.sent.OpenAlerts()); n > 0 {
		badge = styles.ErrText().Render(fmt.Sprintf(" ⚠%d ", n))
	}
	ro := ""
	if a.cfg.Contexts[a.contextName].ReadOnly {
		ro = styles.WarnText().Render("[RO] ")
	}
	mode := ""
	if b := a.modeBadge(); b != "" {
		mode = b + styles.Dim().Render(" · ")
	}
	left := styles.Header().Render("f9s") + "  " + ro + badge + mode + styles.Dim().Render(fmt.Sprintf(
		"%s · flink %s · slots %d/%d · tms %d · jobs %d",
		a.contextName, orDash(a.ov.FlinkVersion), a.ov.SlotsAvailable, a.ov.SlotsTotal,
		a.ov.TaskManagers, a.ov.JobsRunning))
	right := conn
	if a.fetching {
		right = conn + " " + styles.Dim().Render(a.spin.View())
	}
	inner := a.width - 4
	if inner < 1 {
		inner = 1
	}
	gap := inner - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	line := lipgloss.NewStyle().MaxWidth(inner).Render(left + strings.Repeat(" ", gap) + right)
	return styles.PanelBox().Width(a.width - 2).Render(line)
}
