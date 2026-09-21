package analyze

import (
	"fmt"
	"strings"

	"github.com/imankhadiv/f9s/internal/model"
)

// Incremental summarizes how much of the full state each recent checkpoint
// actually uploaded. With incremental RocksDB/ForSt checkpoints the delta is
// a small fraction; a delta near 100% every time means incremental is off or
// compaction is rewriting most SST files between checkpoints.
type Incremental struct {
	Known      bool    // at least one completed checkpoint reported checkpointed_size
	LastDelta  int64   // bytes uploaded by the newest completed checkpoint
	LastFull   int64   // its full state size
	AvgRatio   float64 // mean delta/full over the sampled checkpoints, 0..1
	Samples    int
	Incapable  bool // the backend never uploads deltas (heap/hashmap)
	FullUpload bool // incremental-capable backend uploading ≥90% every time
}

// IncrementalHealth inspects up to the 10 newest completed checkpoints
// (history newest first). backend is CheckpointConfig.StateBackend.
func IncrementalHealth(history []model.Checkpoint, backend string) Incremental {
	var inc Incremental
	inc.Incapable = !strings.Contains(backend, "RocksDB") && !strings.Contains(backend, "ForSt")
	var sum float64
	for _, h := range history {
		if h.Status != "COMPLETED" || h.IsSavepoint || h.StateSize <= 0 || h.CheckpointedSize <= 0 {
			continue
		}
		if !inc.Known {
			inc.Known, inc.LastDelta, inc.LastFull = true, h.CheckpointedSize, h.StateSize
		}
		r := float64(h.CheckpointedSize) / float64(h.StateSize)
		if r > 1 {
			r = 1
		}
		sum += r
		inc.Samples++
		if inc.Samples == 10 {
			break
		}
	}
	if inc.Samples > 0 {
		inc.AvgRatio = sum / float64(inc.Samples)
	}
	// one full upload is normal (first checkpoint, post-restore); flag only a
	// pattern, and only where incremental is possible at all
	inc.FullUpload = !inc.Incapable && inc.Samples >= 3 && inc.AvgRatio >= 0.9
	return inc
}

// Summary renders the incremental line for the checkpoints view.
func (inc Incremental) Summary() string {
	if !inc.Known {
		return ""
	}
	s := fmt.Sprintf("incremental  last uploaded %s of %s (%d%%) · avg %d%% over %d",
		model.FmtBytes(inc.LastDelta), model.FmtBytes(inc.LastFull),
		pct(inc.LastDelta, inc.LastFull), int(inc.AvgRatio*100+0.5), inc.Samples)
	switch {
	case inc.Incapable:
		s += " · heap backend uploads full state every time"
	case inc.FullUpload:
		s += " · ⚑ uploading ~full state every checkpoint — is execution.checkpointing.incremental on?"
	}
	return s
}

func pct(part, whole int64) int {
	if whole <= 0 {
		return 0
	}
	return int(float64(part)/float64(whole)*100 + 0.5)
}

// Retention explains what happens to retained checkpoints when the job ends.
// warn is true when cancelling the job would leave no checkpoint to restore
// from — the footgun that turns a routine cancel into state loss.
func Retention(cfg model.CheckpointConfig) (text string, warn bool) {
	switch {
	case !cfg.Externalization.Enabled:
		return "retention: none — checkpoints are deleted when the job stops (cancel, finish or failure); only savepoints survive", true
	case cfg.Externalization.DeleteOnCancellation:
		return "retention: kept on failure, DELETED on cancel — take a savepoint (s) or stop with savepoint (S) instead of cancelling", true
	default:
		return "retention: checkpoints are retained after cancel", false
	}
}

// FailureStreak counts consecutive FAILED checkpoints from the newest one
// (history newest first; in-progress checkpoints and savepoints are skipped).
func FailureStreak(history []model.Checkpoint) int {
	n := 0
	for _, h := range history {
		if h.IsSavepoint || h.Status == "IN_PROGRESS" {
			continue
		}
		if h.Status != "FAILED" {
			break
		}
		n++
	}
	return n
}

// Tolerance renders the tolerable-failures budget against the current
// streak. level is "ok", "warn" (budget partly spent) or "crit" (the next
// failure fails the job over).
func Tolerance(tolerable, streak int) (text, level string) {
	text = fmt.Sprintf("tolerates %d consecutive failed checkpoint(s) · current streak %d", tolerable, streak)
	switch {
	case streak >= tolerable:
		// at the budget: one more counted failure triggers a failover
		if streak == 0 {
			text += " — any checkpoint failure fails the job over"
			return text, "ok"
		}
		return text + " — the next failure fails the job over", "crit"
	case streak > 0:
		return text, "warn"
	}
	return text, "ok"
}
