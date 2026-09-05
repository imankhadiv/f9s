package analyze

import "fmt"

// Advice is one actionable suggestion derived from live evidence.
type Advice struct {
	Severity string // "warn" or "info"
	Text     string
}

// Advise turns a job's vertex samples into concrete suggestions. freeSlots
// is the cluster's currently available task slots (-1 unknown).
func Advise(vs []VertexSample, freeSlots int) []Advice {
	var out []Advice
	idx := Bottleneck(vs)
	if idx < 0 {
		return nil
	}
	b := vs[idx]

	// Saturated bottleneck → parallelism suggestion with slot math.
	if b.BusyMsPerSec >= 900 && b.Parallelism > 0 {
		proposed := b.Parallelism * 2
		need := proposed - b.Parallelism
		slotNote := ""
		if freeSlots >= 0 {
			if need <= freeSlots {
				slotNote = fmt.Sprintf(" (+%d slots; %d free — fits)", need, freeSlots)
			} else {
				slotNote = fmt.Sprintf(" (+%d slots but only %d free — add TaskManagers first)", need, freeSlots)
			}
		}
		out = append(out, Advice{Severity: "warn", Text: fmt.Sprintf(
			"%q is %d%% busy and the bottleneck — try parallelism %d → %d%s",
			b.Name, int(b.BusyMsPerSec/10), b.Parallelism, proposed, slotNote)})
	}

	// Backpressure reaching a lazy sink → the problem is outside Flink.
	last := vs[len(vs)-1]
	if idx == len(vs)-1 && backpressured(last.Level) && last.BusyMsPerSec >= 0 && last.BusyMsPerSec < 500 {
		out = append(out, Advice{Severity: "warn", Text: fmt.Sprintf(
			"%q is backpressured but only %d%% busy — the external sink (Kafka/DB/HTTP) is the likely limit; parallelism won't help",
			last.Name, int(last.BusyMsPerSec/10))})
	}

	if len(out) == 0 {
		out = append(out, Advice{Severity: "info", Text: fmt.Sprintf(
			"bottleneck %q is not saturated (%d%% busy) — backpressure may be transient; watch the sentinel history",
			b.Name, int(b.BusyMsPerSec/10))})
	}
	return out
}

// CheckpointAdvice inspects completed checkpoint durations (newest first,
// milliseconds) against the configured interval/timeout. Rules:
// sharply rising durations (recent 3 ≥ 2× the 3 before), durations eating
// most of the interval (suggest unaligned barriers), and near-timeout.
func CheckpointAdvice(durations []int64, interval, timeout int64, unaligned bool) []Advice {
	var out []Advice
	if len(durations) == 0 {
		return nil
	}
	latest := durations[0]
	if len(durations) >= 6 {
		recent := (durations[0] + durations[1] + durations[2]) / 3
		before := (durations[3] + durations[4] + durations[5]) / 3
		if before > 0 && recent >= 2*before && recent >= 5000 {
			out = append(out, Advice{Severity: "warn", Text: fmt.Sprintf(
				"checkpoint durations rising sharply (%s → %s) — usually growing state or backpressure; check the state-size trend and the sentinel history",
				fmtMs(before), fmtMs(recent))})
		}
	}
	if timeout > 0 && latest >= timeout*8/10 {
		out = append(out, Advice{Severity: "warn", Text: fmt.Sprintf(
			"latest checkpoint took %s of a %s timeout — the next slowdown will start failing checkpoints",
			fmtMs(latest), fmtMs(timeout))})
	} else if interval > 0 && latest > interval/2 && !unaligned {
		out = append(out, Advice{Severity: "warn", Text: fmt.Sprintf(
			"checkpoints take %s of a %s interval — under backpressure, unaligned checkpoints (execution.checkpointing.unaligned: true) decouple barriers from the data flow",
			fmtMs(latest), fmtMs(interval))})
	}
	return out
}

// fmtMs renders a millisecond duration compactly (12s / 1m05s / 450ms).
func fmtMs(ms int64) string {
	switch {
	case ms >= 60000:
		return fmt.Sprintf("%dm%02ds", ms/60000, ms%60000/1000)
	case ms >= 1000:
		return fmt.Sprintf("%ds", ms/1000)
	default:
		return fmt.Sprintf("%dms", ms)
	}
}
