package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/model"
)

// bandPoll throttles the extra samples behind the status band. They answer
// "is this job in trouble", which changes on the order of seconds, not on
// every 2s refresh tick.
const bandPoll = 10 * time.Second

// jobBand is what the status band needs beyond the job itself: the signals
// that live on other endpoints (restarts, checkpoints, exceptions).
type jobBand struct {
	known    bool
	restarts int   // numRestarts (-1 unknown)
	uptime   int64 // ms since the last restart (Flink's uptime metric; -1 unknown)
	ckpt     model.Checkpoints
	cfg      model.CheckpointConfig
	exc      model.Exceptions
}

type jobBandMsg struct{ band jobBand }

// bandCmd samples the band's extra signals, throttled to bandPoll.
func (d *JobDetail) bandCmd() tea.Cmd {
	if time.Since(d.bandAt) < bandPoll {
		return nil
	}
	d.bandAt = time.Now()
	cl, jid := d.deps.CL, d.jid
	return func() tea.Msg {
		b := jobBand{known: true, restarts: -1, uptime: -1}
		if ms, err := cl.JobMetrics(jid, "numRestarts", "uptime"); err == nil {
			for _, m := range ms {
				v, ok := m.Float()
				if !ok {
					continue
				}
				switch m.ID {
				case "numRestarts":
					b.restarts = int(v)
				case "uptime":
					b.uptime = int64(v)
				}
			}
		}
		b.ckpt, _ = cl.Checkpoints(jid)
		b.cfg, _ = cl.CheckpointConfig(jid)
		b.exc, _ = cl.Exceptions(jid)
		return jobBandMsg{band: b}
	}
}

// bandLine is one rendered line of the band. hint names the key that acts
// on the finding — a banner is the one place an operator is already looking
// when that key becomes worth knowing.
type bandLine struct {
	text  string
	level string // "" info · "warn" · "crit"
	hint  string // e.g. "c for checkpoints"; dropped first when width is tight
}

// renderBandLine fits a line to width, dropping the key hint before the
// finding it teaches.
func renderBandLine(l bandLine, width int) string {
	if width <= 0 {
		return ""
	}
	if l.hint != "" {
		full := l.text + "  ↳ " + l.hint
		if len(full) <= width {
			return full
		}
	}
	return truncate(l.text, width)
}

// lastCheckpoint returns the newest non-savepoint checkpoint.
func lastCheckpoint(cp model.Checkpoints) (model.Checkpoint, bool) {
	for _, h := range cp.History {
		if !h.IsSavepoint && h.Status != "IN_PROGRESS" {
			return h, true
		}
	}
	return model.Checkpoint{}, false
}

// bandLines builds the status band: one summary line when the job is
// healthy, plus one line per abnormal signal, so the page grows with the
// severity of what is wrong. Unknown signals are omitted, never rendered as
// reassuring facts.
func bandLines(job model.Job, b jobBand, freeSlots int, bottleneck string, _ []analyze.Advice) []bandLine {
	var out []bandLine

	// ── summary ────────────────────────────────────────────────────────
	parts := []string{job.State}
	switch {
	case b.known && b.restarts > 0 && b.uptime >= 0:
		// time since the LAST RESTART: "up 4d" while a job restart-loops is
		// the most misleading thing the page could say
		parts = append(parts, fmt.Sprintf("%d restarts", b.restarts),
			"running "+model.FmtDuration(b.uptime))
	case b.known && b.uptime > 0:
		parts = append(parts, "up "+model.FmtDuration(b.uptime))
	case job.Duration > 0:
		parts = append(parts, "up "+model.FmtDuration(job.Duration))
	}
	if last, ok := lastCheckpoint(b.ckpt); ok && last.Status == "COMPLETED" {
		parts = append(parts, "ckpt ✓ "+model.FmtAge(last.TriggerTimestamp))
	}
	if n := len(job.Vertices); n > 0 {
		parts = append(parts, fmt.Sprintf("%d vertices", n))
	}
	switch {
	case freeSlots > 0:
		parts = append(parts, fmt.Sprintf("%d free slots", freeSlots))
	case freeSlots == 0:
		parts = append(parts, "no free slots")
	}
	out = append(out, bandLine{text: strings.Join(parts, " · ")})

	// ── checkpoint trouble ─────────────────────────────────────────────
	if last, ok := lastCheckpoint(b.ckpt); ok && last.Status == "FAILED" {
		streak := analyze.FailureStreak(b.ckpt.History)
		why := firstSentence(last.FailureMessage)
		txt := fmt.Sprintf("⚠ ckpt FAILED %s ago", model.FmtAge(last.TriggerTimestamp))
		if why != "" {
			txt += " (" + why + ")"
		}
		level := "warn"
		if streak > 0 {
			txt += fmt.Sprintf(" · streak %d/%d tolerable", streak, b.cfg.TolerableFailed)
			if streak >= b.cfg.TolerableFailed {
				level = "crit" // the next counted failure fails the job over
			}
		}
		out = append(out, bandLine{text: txt, level: level, hint: "c for the checkpoint history"})
	}

	// ── bottleneck + what to do ────────────────────────────────────────
	if bottleneck != "" {
		// the advice renders on its own line(s) below: keeping this one short
		// leaves room for the key that acts on it
		out = append(out, bandLine{text: "⚑ bottleneck: " + bottleneck, level: "warn",
			hint: "d for the subtask distribution"})
	}

	// ── last failure ───────────────────────────────────────────────────
	if root := firstSentence(b.exc.RootException); root != "" {
		txt := "✗ root: " + root
		if b.exc.Timestamp > 0 {
			txt += " · " + model.FmtAge(b.exc.Timestamp) + " ago"
		}
		out = append(out, bandLine{text: txt, level: "crit", hint: "x for the exception history"})
	}

	// ── tasks not running (slot starvation reads here first) ───────────
	if line, bad := taskStatusLine(job.StatusCounts); bad {
		out = append(out, bandLine{text: line, level: "warn", hint: "t for the subtask states"})
	}
	return out
}

// taskStatusLine summarises status-counts when the tasks are NOT all
// running; bad is false when everything is RUNNING (or nothing is known).
func taskStatusLine(counts map[string]int) (string, bool) {
	total, running := 0, 0
	var others []string
	for state, n := range counts {
		total += n
		if state == "RUNNING" {
			running = n
			continue
		}
		if n > 0 {
			others = append(others, fmt.Sprintf("%d %s", n, state))
		}
	}
	if total == 0 || len(others) == 0 {
		return "", false
	}
	sort.Strings(others)
	return fmt.Sprintf("⧗ %d/%d tasks RUNNING · %s", running, total, strings.Join(others, " · ")), true
}

// firstSentence is the first line of a message, trimmed — exception bodies
// carry whole stack traces and a band line must never grow one.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncate(strings.TrimSpace(s), 96)
}
