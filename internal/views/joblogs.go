package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/ui/styles"
)

type jobLogsMsg struct {
	merged string
	tms    int
	err    error
}

// JobLogs is "kubectl logs" for a Flink job: it finds the TaskManagers
// actually running the job's subtasks, tails each of their logs, and
// timestamp-merges the lines with a per-TM tag.
type JobLogs struct {
	deps  Deps
	jid   string
	name  string
	tms   int
	text  string
	vp    viewport.Model
	ready bool
	err   string
}

// NewJobLogs builds the merged-log view for one job.
func NewJobLogs(deps Deps, jid, name string) *JobLogs {
	return &JobLogs{deps: deps, jid: jid, name: name}
}

func (l *JobLogs) Title() string   { return "logs(" + l.name + ")" }
func (l *JobLogs) Filtering() bool { return false }
func (l *JobLogs) Bindings() []key.Binding {
	return []key.Binding{
		binding("up", "↑↓/pgup", "scroll"),
		binding("r", "r", "reload"),
		binding("esc", "esc", "back"),
	}
}

func (l *JobLogs) Init() tea.Cmd { return l.Refresh() }

func (l *JobLogs) Refresh() tea.Cmd {
	cl := l.deps.CL
	jid := l.jid
	return func() tea.Msg {
		tms, err := cl.JobTaskManagers(jid)
		if err != nil {
			return jobLogsMsg{err: err}
		}
		if len(tms) == 0 {
			return jobLogsMsg{err: fmt.Errorf("no TaskManagers currently run this job's subtasks")}
		}
		type tagged struct {
			tag string
			log string
		}
		var logs []tagged
		for _, id := range tms {
			text, err := cl.TMLog(id)
			if err != nil {
				continue
			}
			logs = append(logs, tagged{tag: shortTM(id), log: tail(text, 400)})
		}
		if len(logs) == 0 {
			return jobLogsMsg{err: fmt.Errorf("could not fetch any TaskManager log")}
		}
		var entries []logEntry
		for _, tl := range logs {
			entries = append(entries, splitEntries(tl.tag, tl.log)...)
		}
		sort.SliceStable(entries, func(a, b int) bool { return entries[a].ts < entries[b].ts })
		if len(entries) > 1000 {
			entries = entries[len(entries)-1000:]
		}
		var b strings.Builder
		tagStyles := []lipgloss.Style{styles.OKText(), styles.WarnText(), styles.Header(), styles.ErrText()}
		tagStyle := map[string]lipgloss.Style{}
		next := 0
		for _, e := range entries {
			st, ok := tagStyle[e.tag]
			if !ok {
				st = tagStyles[next%len(tagStyles)]
				tagStyle[e.tag] = st
				next++
			}
			for i, line := range e.lines {
				tag := st.Render(e.tag)
				if i > 0 {
					tag = strings.Repeat(" ", len(e.tag))
				}
				body := line
				if strings.Contains(line, " ERROR ") {
					body = styles.ErrText().Render(line)
				} else if strings.Contains(line, " WARN ") {
					body = styles.WarnText().Render(line)
				}
				b.WriteString(tag + " │ " + body + "\n")
			}
		}
		return jobLogsMsg{merged: b.String(), tms: len(logs)}
	}
}

// logEntry is one timestamped log record (plus its continuation lines).
type logEntry struct {
	ts    string // "2006-01-02 15:04:05,000" — lexicographically sortable
	tag   string
	lines []string
}

// hasTimestamp reports whether a line starts with a log4j timestamp.
func hasTimestamp(line string) bool {
	if len(line) < 23 {
		return false
	}
	return line[4] == '-' && line[7] == '-' && line[10] == ' ' && line[13] == ':' && line[16] == ':' && line[19] == ','
}

// splitEntries groups a log tail into timestamped entries; continuation
// lines (stack traces) stay attached to their record.
func splitEntries(tag, log string) []logEntry {
	var out []logEntry
	for _, line := range strings.Split(log, "\n") {
		if line == "" {
			continue
		}
		if hasTimestamp(line) {
			out = append(out, logEntry{ts: line[:23], tag: tag, lines: []string{line}})
			continue
		}
		if len(out) > 0 {
			out[len(out)-1].lines = append(out[len(out)-1].lines, line)
		}
	}
	return out
}

// shortTM compacts "172.19.0.3:35933-43126f" to its resource-id suffix.
func shortTM(id string) string {
	if i := strings.LastIndexByte(id, '-'); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (l *JobLogs) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case jobLogsMsg:
		if msg.err != nil {
			l.err = msg.err.Error()
			return l, nil
		}
		l.err = ""
		atBottom := !l.ready || l.vp.AtBottom()
		l.text = msg.merged
		l.tms = msg.tms
		if l.ready {
			l.vp.SetContent(l.text)
			if atBottom {
				l.vp.GotoBottom()
			}
		}
		return l, nil
	case tea.KeyMsg:
		if l.ready {
			var cmd tea.Cmd
			l.vp, cmd = l.vp.Update(msg)
			return l, cmd
		}
	}
	return l, nil
}

func (l *JobLogs) View(width, height int) string {
	right := fmt.Sprintf("merged from %d TaskManager(s) · <r> reloads", l.tms)
	return framed(width, height, "Job logs · "+l.name, -1, right, func(iw, ih int) string {
		out := ""
		used := 0
		if l.err != "" {
			out += styles.ErrText().Render("⚠ "+l.err) + "\n"
			used++
		}
		if !l.ready {
			l.vp = viewport.New(iw, ih-used)
			l.ready = true
			if l.text != "" {
				l.vp.SetContent(l.text)
				l.vp.GotoBottom()
			}
		}
		l.vp.Width = iw
		l.vp.Height = ih - used
		if l.text == "" && l.err == "" {
			l.vp.SetContent(styles.Dim().Render("collecting logs from the job's TaskManagers…"))
		}
		return out + l.vp.View()
	})
}
