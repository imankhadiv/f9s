// Package uitable is f9s's table component. bubbles/table cannot style
// individual cells (ANSI inside a cell breaks its width math and the
// selected-row band), so this renders cells plain, truncates them to width,
// and only then applies styles — per-cell color with correct alignment, and
// an unbroken selection band.
package uitable

import (
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/sahilm/fuzzy"
)

const colGap = 2

// Column is a table column; Width < 0 marks the one flexible column that
// absorbs the remaining terminal width. Right right-aligns the column (for
// numeric values), header included.
type Column struct {
	Title string
	Width int
	Right bool
}

// Cell is one table cell: plain text plus the style applied after layout.
type Cell struct {
	Text  string
	Style lipgloss.Style
}

// Plain builds an unstyled cell.
func Plain(text string) Cell { return Cell{Text: text} }

// Styled builds a styled cell.
func Styled(text string, st lipgloss.Style) Cell { return Cell{Text: text, Style: st} }

// Model is the table state: columns, rows, cursor, scroll window.
type Model struct {
	HeaderStyle   lipgloss.Style
	BorderStyle   lipgloss.Style
	SelectedStyle lipgloss.Style

	cols    []Column
	rows    [][]Cell
	keys    []string               // optional per-row identity for cursor stability
	tints   map[int]lipgloss.Style // whole-row fg tint by original row index
	sortCol int                    // active sort column, -1 for none
	sortGly string                 // sort-arrow glyph appended to that column's title
	filter  string
	vis     []int // visible row indexes under the filter; nil = all rows
	cursor  int   // position within the visible list
	offset  int
	width   int
	height  int
}

// visibleIdx maps a visible position to its original row index.
func (m *Model) visibleIdx(pos int) int {
	if m.vis == nil {
		return pos
	}
	if pos < 0 || pos >= len(m.vis) {
		return 0
	}
	return m.vis[pos]
}

// visibleCount is how many rows the filter lets through.
func (m *Model) visibleCount() int {
	if m.vis == nil {
		return len(m.rows)
	}
	return len(m.vis)
}

// SetFilter applies a case-insensitive substring filter across every cell;
// empty clears it. The cursor stays on the selected row when it survives.
func (m *Model) SetFilter(q string) {
	prev := m.Cursor()
	m.filter = strings.ToLower(q)
	m.refilter()
	if m.filter != "" {
		for pos := range m.vis {
			if m.vis[pos] == prev {
				m.cursor = pos
				break
			}
		}
	} else {
		m.cursor = prev
	}
	m.clamp()
}

// Filter returns the active filter string.
func (m *Model) Filter() string { return m.filter }

func (m *Model) refilter() {
	if m.filter == "" {
		m.vis = nil
		return
	}
	m.vis = []int{} // non-nil: an empty result means "nothing matches"
	match := m.matcher()
	for i, row := range m.rows {
		hay := rowHaystack(row)
		if match(hay) {
			m.vis = append(m.vis, i)
		}
	}
}

// matcher parses the filter query into a predicate. Prefixes: "!" inverts,
// "-f " fuzzy-matches; a query with regex metacharacters compiles as a
// case-insensitive regexp (falling back to substring if it won't compile);
// everything else is a plain substring — k9s-grade / filtering.
func (m *Model) matcher() func(string) bool {
	q := m.filter // already lowercased by SetFilter
	inverse := false
	if strings.HasPrefix(q, "!") {
		inverse, q = true, q[1:]
	}
	base := func(hay string) bool { return strings.Contains(hay, q) }
	switch {
	case strings.HasPrefix(q, "-f "):
		pat := strings.TrimSpace(q[3:])
		base = func(hay string) bool { return len(fuzzy.Find(pat, []string{hay})) > 0 }
	case strings.ContainsAny(q, `.*+?()[]{}|^$\`):
		if re, err := regexp.Compile("(?i)" + q); err == nil {
			base = func(hay string) bool { return re.MatchString(hay) }
		}
	}
	if inverse {
		return func(hay string) bool { return !base(hay) }
	}
	return base
}

// rowHaystack joins a row's cells into one lowercased string to match against.
func rowHaystack(row []Cell) string {
	parts := make([]string, len(row))
	for i, c := range row {
		parts[i] = c.Text
	}
	return strings.ToLower(strings.Join(parts, " "))
}

// New builds a table with the given columns.
func New(cols []Column) *Model {
	return &Model{cols: cols, sortCol: -1}
}

// SetColumns replaces the column set (e.g. on resize).
func (m *Model) SetColumns(cols []Column) { m.cols = cols }

// SetSort marks the active sort column and the arrow glyph to show in its
// header (pass an empty glyph, or col < 0, to clear it).
func (m *Model) SetSort(col int, glyph string) {
	m.sortCol, m.sortGly = col, glyph
}

// SetRowTints sets a whole-row foreground tint by ORIGINAL row index — used to
// fade completed/canceled rows and flag failed ones so lifecycle reads at a
// glance. The selection band still overrides a tinted row. Pass nil to clear.
func (m *Model) SetRowTints(t map[int]lipgloss.Style) { m.tints = t }

// SetRows replaces the data and clamps the cursor.
func (m *Model) SetRows(rows [][]Cell) {
	m.rows = rows
	m.keys = nil
	m.refilter()
	m.clamp()
}

// SetRowsKeyed replaces the data and keeps the cursor on the row with the
// same key as the previously selected one, so refresh-driven re-sorts don't
// silently move the selection to a different entity.
func (m *Model) SetRowsKeyed(rows [][]Cell, keys []string) {
	prev := ""
	if orig := m.Cursor(); orig >= 0 && orig < len(m.keys) {
		prev = m.keys[orig]
	}
	m.rows, m.keys = rows, keys
	m.refilter()
	if prev != "" {
		for pos := 0; pos < m.visibleCount(); pos++ {
			if keys[m.visibleIdx(pos)] == prev {
				m.cursor = pos
				m.clamp()
				return
			}
		}
	}
	m.clamp()
}

func (m *Model) clamp() {
	if m.cursor >= m.visibleCount() {
		m.cursor = m.visibleCount() - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// SetSize sets the render viewport in cells.
func (m *Model) SetSize(width, height int) {
	m.width, m.height = width, height
}

// Cursor returns the selected row's ORIGINAL index (0 when empty), so
// callers can keep indexing their unfiltered backing slice.
func (m *Model) Cursor() int { return m.visibleIdx(m.cursor) }

// SetCursor moves the cursor to an ORIGINAL row index (the same space
// Cursor reports), ignoring rows the filter hides.
func (m *Model) SetCursor(row int) {
	if m.vis == nil {
		m.cursor = row
		m.clamp()
		return
	}
	for pos, orig := range m.vis {
		if orig == row {
			m.cursor = pos
			m.clamp()
			return
		}
	}
}

// Len returns the number of visible rows (all rows without a filter).
func (m *Model) Len() int { return m.visibleCount() }

func (m *Model) visible() int {
	v := m.height - 2 // header + separator
	if v < 1 {
		v = 1
	}
	return v
}

// Update moves the cursor on navigation keys; other messages are ignored.
func (m *Model) Update(msg tea.Msg) {
	key, ok := msg.(tea.KeyMsg)
	if !ok || m.visibleCount() == 0 {
		return
	}
	switch key.String() {
	case "up", "k":
		m.cursor--
	case "down", "j":
		m.cursor++
	case "pgup":
		m.cursor -= m.visible()
	case "pgdown":
		m.cursor += m.visible()
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = m.visibleCount() - 1
	default:
		return
	}
	m.clamp()
}

// resolved returns concrete column widths, sizing the flex column from the
// current viewport width.
func (m *Model) resolved() []int {
	ws := make([]int, len(m.cols))
	flexIdx := -1
	used := 1 // leading space
	for i, c := range m.cols {
		if c.Width < 0 {
			flexIdx = i
			continue
		}
		ws[i] = c.Width
		used += c.Width + colGap
	}
	if flexIdx >= 0 {
		w := m.width - used - colGap
		if w < 20 {
			w = 20
		}
		ws[flexIdx] = w
	}
	return ws
}

// fit truncates (with ellipsis) or pads s to exactly w display cells.
func fit(s string, w int) string { return fitAlign(s, w, false) }

// fitAlign is fit with optional right-alignment (pad on the left).
func fitAlign(s string, w int, right bool) string {
	if w < 0 {
		w = 0
	}
	if runewidth.StringWidth(s) > w {
		s = runewidth.Truncate(s, w, "…")
	}
	pad := strings.Repeat(" ", w-runewidth.StringWidth(s))
	if right {
		return pad + s
	}
	return s + pad
}

// View renders the table padded to its full height so footers stay pinned.
func (m *Model) View() string {
	ws := m.resolved()
	gap := strings.Repeat(" ", colGap)

	titles := make([]string, len(m.cols))
	for i, c := range m.cols {
		t := c.Title
		if i == m.sortCol && m.sortGly != "" {
			t += m.sortGly
		}
		titles[i] = fitAlign(t, ws[i], c.Right)
	}
	lines := []string{
		" " + m.HeaderStyle.Render(strings.Join(titles, gap)),
		m.BorderStyle.Render(strings.Repeat("─", max(m.width, 1))),
	}

	// keep the cursor inside the scroll window
	vis := m.visible()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+vis {
		m.offset = m.cursor - vis + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}

	end := m.offset + vis
	if end > m.visibleCount() {
		end = m.visibleCount()
	}
	for r := m.offset; r < end; r++ {
		cells := m.rows[m.visibleIdx(r)]
		parts := make([]string, len(m.cols))
		if r == m.cursor {
			// selection band: style the whole plain line so it never breaks
			for i := range m.cols {
				parts[i] = fitAlign(cellText(cells, i), ws[i], m.cols[i].Right)
			}
			lines = append(lines, m.SelectedStyle.Render(" "+strings.Join(parts, gap)))
			continue
		}
		if tint, ok := m.tints[m.visibleIdx(r)]; ok {
			// whole-row lifecycle tint: render plain, then tint the line
			for i := range m.cols {
				parts[i] = fitAlign(cellText(cells, i), ws[i], m.cols[i].Right)
			}
			lines = append(lines, tint.Render(" "+strings.Join(parts, gap)))
			continue
		}
		for i := range m.cols {
			parts[i] = cells[min(i, len(cells)-1)].Style.Render(fitAlign(cellText(cells, i), ws[i], m.cols[i].Right))
		}
		lines = append(lines, " "+strings.Join(parts, gap))
	}
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func cellText(cells []Cell, i int) string {
	if i >= len(cells) {
		return ""
	}
	return cells[i].Text
}
