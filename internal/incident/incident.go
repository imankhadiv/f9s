// Package incident gives investigations organizational memory: every
// completed diagnosis is fingerprinted and stored, and a new incident that
// matches a past one starts from what was learned — "this happened before,
// here is what we concluded and what the operator did next."
package incident

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/imankhadiv/f9s/internal/ledger"
)

// Fingerprint identifies an incident's shape.
type Fingerprint struct {
	Job        string `json:"job"`
	Exception  string `json:"exception"`  // exception class from the evidence
	Bottleneck string `json:"bottleneck"` // computed bottleneck vertex
	Kind       string `json:"kind"`       // alert kind for watch-mode triggers
}

// Record is one remembered incident.
type Record struct {
	Time       time.Time   `json:"time"`
	JID        string      `json:"jid"`
	FP         Fingerprint `json:"fp"`
	Hypothesis string      `json:"hypothesis"`
}

var (
	excRe = regexp.MustCompile(`(?m)^(?:ROOT EXCEPTION:\s*\n?|EXCEPTION #\d+ )([\w.$]+(?:Exception|Error))`)
	botRe = regexp.MustCompile(`(?m)^COMPUTED BOTTLENECK: "([^"]+)"`)
)

// Extract builds a fingerprint from the evidence seed.
func Extract(seed, job, kind string) Fingerprint {
	fp := Fingerprint{Job: job, Kind: kind}
	if m := excRe.FindStringSubmatch(seed); m != nil {
		fp.Exception = m[1]
	}
	if m := botRe.FindStringSubmatch(seed); m != nil {
		fp.Bottleneck = m[1]
	}
	return fp
}

func file(ctx string) string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join("incidents", ctx+".jsonl")
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "f9s", "incidents", ctx+".jsonl")
}

// Append remembers a completed investigation (best-effort).
func Append(ctx string, r Record) {
	if r.Hypothesis == "" {
		return
	}
	path := file(ctx)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	f.Write(append(data, '\n'))
}

// HypothesisOf extracts the memorable core of an answer: the HYPOTHESIS
// section when present, else the first non-empty line.
func HypothesisOf(answer string) string {
	for _, line := range strings.Split(answer, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if h, ok := strings.CutPrefix(line, "HYPOTHESIS:"); ok {
			return strings.TrimSpace(h)
		}
		if len(line) > 200 {
			line = line[:199] + "…"
		}
		return line
	}
	return ""
}

// matches scores fingerprint similarity: exception class is the strongest
// signal, then job+kind, then bottleneck vertex, and finally — for records
// fresh enough to still be the same operational situation — the job alone.
func matches(a, b Fingerprint, recordedAt time.Time) bool {
	if a.Exception != "" && a.Exception == b.Exception {
		return true
	}
	if a.Job == b.Job && a.Kind != "" && a.Kind == b.Kind {
		return true
	}
	if a.Bottleneck != "" && a.Bottleneck == b.Bottleneck && a.Job == b.Job {
		return true
	}
	if a.Job == b.Job && time.Since(recordedAt) < 7*24*time.Hour {
		return true
	}
	return false
}

// Recall finds the most recent similar past incident, if any.
func Recall(ctx string, fp Fingerprint) *Record {
	f, err := os.Open(file(ctx))
	if err != nil {
		return nil
	}
	defer f.Close()
	var best *Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 256*1024)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if matches(fp, r.FP, r.Time) {
			rr := r
			best = &rr // newest match wins (file is chronological)
		}
	}
	return best
}

// Inject prepends the recalled incident (plus the operator actions that
// followed it, from the ledger) to a new investigation seed.
func Inject(ctx, seed, job, kind string) string {
	fp := Extract(seed, job, kind)
	past := Recall(ctx, fp)
	if past == nil {
		return seed
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SIMILAR PAST INCIDENT (%s, job %q", past.Time.Format("2006-01-02 15:04"), past.FP.Job)
	if past.FP.Exception != "" {
		fmt.Fprintf(&b, ", same exception class %s", past.FP.Exception)
	}
	b.WriteString("):\n")
	fmt.Fprintf(&b, "  prior conclusion: %s\n", past.Hypothesis)
	if entries, err := ledger.List(ctx); err == nil {
		for _, e := range entries {
			if e.Job == past.FP.Job && e.Time.After(past.Time) && e.Time.Before(past.Time.Add(30*time.Minute)) {
				fmt.Fprintf(&b, "  operator action within 30m: %s → %s\n", e.Action, e.Path)
			}
		}
	}
	b.WriteString("Verify whether the same cause applies before exploring alternatives.\n\n")
	return b.String() + seed
}
