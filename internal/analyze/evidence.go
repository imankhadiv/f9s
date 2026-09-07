package analyze

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/model"
)

// Evidence is the curated bundle explain-with-AI sends to a model: small,
// high-signal, and human-readable — the same facts an SRE would gather.
type Evidence struct {
	Text string
	// Mapping is anonymized-placeholder → real value, for de-anonymizing
	// the model's answer before display.
	Mapping map[string]string
}

// BuildEvidence gathers the diagnosis bundle for one job.
func BuildEvidence(cl *client.Client, jid string) (string, error) {
	var b strings.Builder
	job, err := cl.Job(jid)
	if err != nil {
		return "", fmt.Errorf("job: %w", err)
	}
	fmt.Fprintf(&b, "JOB %q state=%s uptime=%s vertices=%d\n",
		job.Name, job.State, model.FmtDuration(job.Duration), len(job.Vertices))

	// restarts
	if ms, err := cl.JobMetrics(jid, "numRestarts"); err == nil {
		for _, m := range ms {
			if m.ID == "numRestarts" {
				fmt.Fprintf(&b, "RESTARTS: %s\n", m.Value)
			}
		}
	}

	// checkpoints
	if cp, err := cl.Checkpoints(jid); err == nil {
		fmt.Fprintf(&b, "CHECKPOINTS: completed=%d failed=%d in_progress=%d restored=%d\n",
			cp.Counts.Completed, cp.Counts.Failed, cp.Counts.InProgress, cp.Counts.Restored)
		for i, h := range cp.History {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  ckpt id=%d status=%s savepoint=%v age=%s duration=%s size=%s acked=%d/%d",
				h.ID, h.Status, h.IsSavepoint, model.FmtAge(h.TriggerTimestamp),
				model.FmtDuration(h.EndToEndDuration), model.FmtBytes(h.StateSize),
				h.NumAcknowledged, h.NumSubtasks)
			if h.FailureMessage != "" {
				fmt.Fprintf(&b, " failure=%q", h.FailureMessage)
			}
			b.WriteString("\n")
		}

		// RECOVERY POINT: the rollback net any mutation leans on. State it
		// explicitly so a proposal can reason about it rather than blindly
		// assuming the job is recoverable. "completed=0" alone is ambiguous
		// (checkpointing off vs a brand-new job) — CheckpointConfig disambiguates.
		recovery := "NONE — no completed checkpoint; a restart would lose in-flight state"
		for _, h := range cp.History {
			if h.Status == "COMPLETED" {
				kind := "checkpoint"
				if h.IsSavepoint {
					kind = "savepoint"
				}
				recovery = fmt.Sprintf("latest %s age=%s (id=%d)", kind, model.FmtAge(h.TriggerTimestamp), h.ID)
				break
			}
		}
		// Flink reports interval = math.MaxInt64 as its sentinel for "periodic
		// checkpointing disabled" (checkpoints only on manual/savepoint trigger).
		// A brand-new job that simply hasn't fetched config yet stays "unknown".
		enabled, interval, known := false, int64(0), false
		if cfg, err := cl.CheckpointConfig(jid); err == nil {
			interval, known = cfg.Interval, true
			enabled = cfg.Interval > 0 && cfg.Interval < math.MaxInt64
		}
		switch {
		case known && !enabled:
			fmt.Fprintf(&b, "RECOVERY POINT: periodic checkpointing DISABLED — no automatic rollback; recovery=%s\n", recovery)
		case known:
			fmt.Fprintf(&b, "RECOVERY POINT: checkpointing on (interval=%s) — recovery=%s\n", model.FmtDuration(interval), recovery)
		default:
			fmt.Fprintf(&b, "RECOVERY POINT: recovery=%s\n", recovery)
		}
	}

	// per-vertex backpressure / busy / watermarks, plus the computed bottleneck
	samples := make([]VertexSample, len(job.Vertices))
	for i, v := range job.Vertices {
		level, busy := "", -1.0
		if bp, err := cl.Backpressure(jid, v.ID); err == nil {
			level = bp.Level()
		}
		if bz, err := cl.VertexBusy(jid, v.ID); err == nil {
			busy = bz
		}
		samples[i] = VertexSample{ID: v.ID, Name: v.Name, Level: level, BusyMsPerSec: busy, Parallelism: v.Parallelism}
		fmt.Fprintf(&b, "VERTEX %q parallelism=%d status=%s backpressure=%s busy=%.0fms/s in=%d out=%d\n",
			v.Name, v.Parallelism, v.Status, orUnknown(level), busy,
			v.Metrics.ReadRecords, v.Metrics.WriteRecords)
	}
	if idx := Bottleneck(samples); idx >= 0 {
		fmt.Fprintf(&b, "COMPUTED BOTTLENECK: %q (busy but not backpressured downstream of backpressure)\n", job.Vertices[idx].Name)
	}
	slots := -1
	if ov, err := cl.Overview(); err == nil {
		slots = ov.SlotsAvailable
	}
	for _, adv := range Advise(samples, slots) {
		fmt.Fprintf(&b, "SUGGESTION: %s\n", adv.Text)
	}

	// exceptions
	if ex, err := cl.Exceptions(jid); err == nil {
		if ex.RootException != "" {
			fmt.Fprintf(&b, "ROOT EXCEPTION:\n%s\n", firstLines(ex.RootException, 12))
		}
		for i, e := range ex.History.Entries {
			if i >= 3 {
				break
			}
			labels := ""
			for k, v := range e.FailureLabels {
				labels += fmt.Sprintf(" label:%s=%s", k, v)
			}
			fmt.Fprintf(&b, "EXCEPTION #%d %s task=%q at=%s%s\n%s\n",
				i+1, e.ExceptionName, e.TaskName, e.Location, labels, firstLines(e.Stacktrace, 8))
		}
	}
	return b.String(), nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append(lines[:n], "  …(truncated)")
	}
	return strings.Join(lines, "\n")
}

var (
	ipRe   = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(?::\d+)?\b`)
	pathRe = regexp.MustCompile(`\b(?:s3a?|hdfs|gs|abfss?|oss)://?[^\s"']+|\bfile://?[^\s"']+`)
	// urlCredRe masks userinfo in any scheme://user:pass@ URL.
	urlCredRe = regexp.MustCompile(`(://)([^/\s:@]{1,64}):([^@\s]{1,128})@`)
	// kvSecretRe masks values of password/secret/token-style keys (covers
	// JAAS blobs, connector props, and config dumps in stack traces).
	kvSecretRe = regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[-_]?key|access[-_]?key|secret[-_]?key)("?\s*[=:]\s*"?)([^\s,;"'&]{1,256})`)
	// authHeaderRe masks Authorization/Bearer header values.
	authHeaderRe = regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*(?:bearer\s+)?|bearer\s+)([A-Za-z0-9._~+/=-]{8,})`)
	awsKeyRe     = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	// fqdnRe: internal hostnames. Hyphens are illegal in Java identifiers,
	// so requiring a hyphenated first label never scrubs class names
	// (kafka-broker-3.prod.internal matches; org.apache.flink.JobMaster
	// does not).
	fqdnRe = regexp.MustCompile(`\b[a-z0-9]+(?:-[a-z0-9]+)+(?:\.[a-z0-9-]+)+(?::\d{2,5})?\b`)
	// hostPortRe: dotted host with an explicit port (source-file line refs
	// like Foo.java:123 excluded).
	hostPortRe = regexp.MustCompile(`\b[a-z][a-z0-9.-]*\.[a-z]{2,}:\d{2,5}\b`)
	fileLineRe = regexp.MustCompile(`\.(java|scala|kt|py|go):\d+$`)
)

// Scrubber anonymizes text with a mapping that persists across calls, so
// every step of an agent loop is scrubbed consistently and the final answer
// can be restored.
type Scrubber struct {
	names   []string
	mapping map[string]string
	n       int
}

// NewScrubber builds a scrubber that always masks the given names.
func NewScrubber(names []string) *Scrubber {
	return &Scrubber{names: names, mapping: map[string]string{}}
}

// AddName registers another name to mask, without resetting the placeholder
// mapping — so a long-lived scrubber (e.g. the AI panel, as the operator
// navigates between jobs) keeps stable placeholders while learning new names.
func (s *Scrubber) AddName(name string) {
	if name == "" {
		return
	}
	for _, n := range s.names {
		if n == name {
			return
		}
	}
	s.names = append(s.names, name)
}

func (s *Scrubber) replace(real, kind string) string {
	for ph, v := range s.mapping {
		if v == real {
			return ph
		}
	}
	s.n++
	ph := fmt.Sprintf("<%s-%d>", kind, s.n)
	s.mapping[ph] = real
	return ph
}

// Scrub masks credentials, names, IPs, hostnames, and storage paths,
// reusing prior placeholders so multi-step agent loops stay consistent.
// Credentials go first so secrets inside URLs never survive as part of a
// larger host/path match.
func (s *Scrubber) Scrub(text string) string {
	text = urlCredRe.ReplaceAllString(text, "$1<cred>@")
	text = kvSecretRe.ReplaceAllString(text, "$1$2<cred>")
	text = authHeaderRe.ReplaceAllString(text, "$1<cred>")
	text = awsKeyRe.ReplaceAllString(text, "<cred>")
	for _, name := range s.names {
		if name == "" {
			continue
		}
		text = strings.ReplaceAll(text, name, s.replace(name, "name"))
	}
	text = ipRe.ReplaceAllStringFunc(text, func(m string) string { return s.replace(m, "host") })
	text = pathRe.ReplaceAllStringFunc(text, func(m string) string { return s.replace(m, "path") })
	text = fqdnRe.ReplaceAllStringFunc(text, func(m string) string { return s.replace(m, "host") })
	text = hostPortRe.ReplaceAllStringFunc(text, func(m string) string {
		if fileLineRe.MatchString(m) {
			return m
		}
		return s.replace(m, "host")
	})
	return text
}

// Restore de-anonymizes text using the accumulated mapping.
func (s *Scrubber) Restore(text string) string { return Deanonymize(text, s.mapping) }

// Mapping exposes the accumulated placeholder → real mapping.
func (s *Scrubber) Mapping() map[string]string { return s.mapping }

// Anonymize replaces identifying values (extra names plus detected IPs and
// storage paths) with stable placeholders, returning the reverse mapping so
// the model's answer can be de-anonymized before display.
func Anonymize(text string, names []string) (string, map[string]string) {
	sc := NewScrubber(names)
	out := sc.Scrub(text)
	return out, sc.mapping
}

// Deanonymize restores real values in the model's answer.
func Deanonymize(text string, mapping map[string]string) string {
	for ph, real := range mapping {
		text = strings.ReplaceAll(text, ph, real)
	}
	return text
}
