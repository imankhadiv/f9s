package views

import (
	"strings"
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/model"
)

func healthyBand() (model.Job, jobBand) {
	now := time.Now().UnixMilli()
	job := model.Job{State: "RUNNING", Duration: 4 * 24 * 3600 * 1000,
		Vertices:     []model.Vertex{{ID: "a"}, {ID: "b"}},
		StatusCounts: map[string]int{"RUNNING": 8}}
	b := jobBand{
		known: true, restarts: 0, uptime: 4 * 24 * 3600 * 1000,
		ckpt: model.Checkpoints{History: []model.Checkpoint{
			{ID: 9, Status: "COMPLETED", TriggerTimestamp: now - 12_000},
		}},
	}
	return job, b
}

func bandText(lines []bandLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.text + "\n")
	}
	return b.String()
}

func TestBandIsOneLineWhenHealthy(t *testing.T) {
	job, b := healthyBand()
	lines := bandLines(job, b, 3, "", nil)
	if len(lines) != 1 {
		t.Fatalf("healthy job produced %d lines:\n%s", len(lines), bandText(lines))
	}
	for _, want := range []string{"RUNNING", "up 4d00h", "ckpt ✓", "3 free slots"} {
		if !strings.Contains(lines[0].text, want) {
			t.Errorf("summary missing %q: %q", want, lines[0].text)
		}
	}
}

// Uptime must be time since the LAST RESTART, not since submission: a job
// that restarted 40s ago must not read "up 4d".
func TestBandShowsRestartLoopNotSubmissionUptime(t *testing.T) {
	job, b := healthyBand()
	job.State = "RESTARTING"
	b.restarts, b.uptime = 3, 40_000 // 3 restarts, running 40s

	lines := bandLines(job, b, 3, "", nil)
	txt := bandText(lines)
	if strings.Contains(txt, "4d") {
		t.Errorf("band still shows submission uptime:\n%s", txt)
	}
	for _, want := range []string{"RESTARTING", "3 restarts", "40s"} {
		if !strings.Contains(txt, want) {
			t.Errorf("band missing %q:\n%s", want, txt)
		}
	}
}

func TestBandFlagsCheckpointFailureWithBudget(t *testing.T) {
	job, b := healthyBand()
	now := time.Now().UnixMilli()
	b.ckpt.History = []model.Checkpoint{
		{ID: 11, Status: "FAILED", TriggerTimestamp: now - 240_000, FailureMessage: "Checkpoint expired before completing"},
		{ID: 10, Status: "FAILED", TriggerTimestamp: now - 270_000},
		{ID: 9, Status: "COMPLETED", TriggerTimestamp: now - 300_000},
	}
	b.cfg.TolerableFailed = 2

	lines := bandLines(job, b, 3, "", nil)
	txt := bandText(lines)
	for _, want := range []string{"ckpt FAILED", "4m00s ago", "expired", "2/2"} {
		if !strings.Contains(txt, want) {
			t.Errorf("band missing %q:\n%s", want, txt)
		}
	}
	if lines[1].level != "crit" {
		t.Errorf("a spent failure budget is critical, got %q", lines[1].level)
	}
}

func TestBandShowsRootExceptionAndBottleneck(t *testing.T) {
	job, b := healthyBand()
	b.exc = model.Exceptions{
		RootException: "java.util.concurrent.TimeoutException: Heartbeat of TaskManager timed out\n\tat org.apache…",
		Timestamp:     time.Now().UnixMilli() - 60_000,
	}
	lines := bandLines(job, b, 0, "Window(5m)", []analyze.Advice{{Severity: "warn", Text: "try parallelism 8 → 16"}})
	txt := bandText(lines)
	for _, want := range []string{"⚑ bottleneck", "Window(5m)", "TimeoutException", "1m00s ago", "no free slots"} {
		if !strings.Contains(txt, want) {
			t.Errorf("band missing %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "\tat org.apache") {
		t.Errorf("stack trace leaked into the band:\n%s", txt)
	}
	if strings.Contains(txt, "try parallelism") {
		t.Errorf("advice belongs on its own line, not in the band:\n%s", txt)
	}
}

func TestBandShowsTasksNotRunning(t *testing.T) {
	job, b := healthyBand()
	job.StatusCounts = map[string]int{"RUNNING": 6, "DEPLOYING": 2}
	lines := bandLines(job, b, 0, "", nil)
	txt := bandText(lines)
	if !strings.Contains(txt, "6/8 tasks RUNNING") || !strings.Contains(txt, "2 DEPLOYING") {
		t.Errorf("band does not show the deploying tasks:\n%s", txt)
	}
}

// Before the extra samples land the band must not invent a verdict.
func TestBandBeforeItHasData(t *testing.T) {
	job, _ := healthyBand()
	lines := bandLines(job, jobBand{}, -1, "", nil)
	if len(lines) != 1 {
		t.Fatalf("unsampled band produced %d lines:\n%s", len(lines), bandText(lines))
	}
	txt := lines[0].text
	for _, never := range []string{"ckpt ✓", "free slots", "0 restarts"} {
		if strings.Contains(txt, never) {
			t.Errorf("unknown signal rendered as fact (%q): %q", never, txt)
		}
	}
}

func TestBandNeverExceedsFiveLines(t *testing.T) {
	job, b := healthyBand()
	job.State = "RESTARTING"
	job.StatusCounts = map[string]int{"RUNNING": 1, "CREATED": 7}
	b.restarts, b.uptime = 9, 5_000
	b.ckpt.History = []model.Checkpoint{{ID: 3, Status: "FAILED", TriggerTimestamp: time.Now().UnixMilli(), FailureMessage: "boom"}}
	b.exc = model.Exceptions{RootException: "java.lang.OutOfMemoryError", Timestamp: time.Now().UnixMilli()}
	lines := bandLines(job, b, 0, "Window", []analyze.Advice{{Text: "a"}, {Text: "b"}, {Text: "c"}})
	if len(lines) > 5 {
		t.Fatalf("band grew to %d lines:\n%s", len(lines), bandText(lines))
	}
}

func TestFrameStatusIsJustTheState(t *testing.T) {
	d := NewJobDetail(Deps{}, "j", "orders")
	d.job = model.Job{State: "RUNNING", Duration: 4 * 24 * 3600 * 1000}
	d.band = jobBand{known: true, restarts: 2, uptime: 3_840_000}
	if got := d.frameStatus(); got != "RUNNING" {
		t.Fatalf("frame status = %q; the band owns the detail", got)
	}
}

// A banner should teach the key that acts on it: over half of each page's
// keys are invisible until the operator opens the help overlay.
func TestBandLinesCarryTheirKey(t *testing.T) {
	job, b := healthyBand()
	now := time.Now().UnixMilli()
	b.ckpt.History = []model.Checkpoint{{ID: 3, Status: "FAILED", TriggerTimestamp: now, FailureMessage: "expired"}}
	b.exc = model.Exceptions{RootException: "java.lang.OutOfMemoryError", Timestamp: now}
	job.StatusCounts = map[string]int{"RUNNING": 1, "DEPLOYING": 3}

	lines := bandLines(job, b, 2, "Window(5m)", []analyze.Advice{{Text: "try parallelism 8 → 16"}})
	hints := map[string]string{}
	for _, l := range lines {
		hints[l.text] = l.hint
	}
	for text, want := range map[string]string{
		"ckpt FAILED":   "c", // the checkpoints view
		"bottleneck":    "d", // the skew distribution
		"root:":         "x", // exceptions
		"tasks RUNNING": "t", // subtasks
	} {
		found := false
		for line, hint := range hints {
			if strings.Contains(line, text) {
				found = true
				if !strings.Contains(hint, want) {
					t.Errorf("line %q hints %q, want the %q key", line, hint, want)
				}
			}
		}
		if !found {
			t.Errorf("no band line matched %q", text)
		}
	}
	if hints[lines[0].text] != "" {
		t.Errorf("the healthy summary has no action to teach, got hint %q", hints[lines[0].text])
	}
}

// The hint is the first thing dropped when the terminal is narrow: the
// finding matters more than the lesson.
func TestBandHintDroppedBeforeTheFinding(t *testing.T) {
	line := bandLine{text: "⚠ ckpt FAILED 4m ago (Checkpoint expired before completing)", hint: "c for checkpoints"}
	wide := renderBandLine(line, 120)
	if !strings.Contains(wide, "ckpt FAILED") || !strings.Contains(wide, "c for checkpoints") {
		t.Fatalf("wide render = %q", wide)
	}
	narrow := renderBandLine(line, 50)
	if strings.Contains(narrow, "for checkpoints") {
		t.Errorf("narrow render kept the hint: %q", narrow)
	}
	if !strings.Contains(narrow, "ckpt FAILED") {
		t.Errorf("narrow render lost the finding: %q", narrow)
	}
}
