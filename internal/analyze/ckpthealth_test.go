package analyze

import (
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/model"
)

func ck(status string, full, delta int64) model.Checkpoint {
	return model.Checkpoint{Status: status, StateSize: full, CheckpointedSize: delta}
}

func TestIncrementalHealth(t *testing.T) {
	tests := []struct {
		name     string
		hist     []model.Checkpoint
		backend  string
		wantFull bool
		wantInc  bool
		wantPct  int
	}{
		{"healthy incremental", []model.Checkpoint{ck("COMPLETED", 1000, 50), ck("COMPLETED", 1000, 80), ck("COMPLETED", 900, 60)},
			"EmbeddedRocksDBStateBackend", false, false, 5},
		{"rocksdb uploading everything", []model.Checkpoint{ck("COMPLETED", 1000, 990), ck("COMPLETED", 1000, 1000), ck("COMPLETED", 1000, 950)},
			"EmbeddedRocksDBStateBackend", true, false, 99},
		{"heap backend is full by design", []model.Checkpoint{ck("COMPLETED", 1000, 1000), ck("COMPLETED", 1000, 1000), ck("COMPLETED", 1000, 1000)},
			"HashMapStateBackend", false, true, 100},
		{"single full upload is not a pattern", []model.Checkpoint{ck("COMPLETED", 1000, 1000), ck("FAILED", 0, 0)},
			"ForStStateBackend", false, false, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inc := IncrementalHealth(tt.hist, tt.backend)
			if !inc.Known || inc.FullUpload != tt.wantFull || inc.Incapable != tt.wantInc {
				t.Fatalf("got %+v", inc)
			}
			if got := pct(inc.LastDelta, inc.LastFull); got != tt.wantPct {
				t.Fatalf("last pct = %d, want %d", got, tt.wantPct)
			}
			if tt.wantFull && !strings.Contains(inc.Summary(), "incremental on?") {
				t.Fatalf("summary lacks the hint: %s", inc.Summary())
			}
		})
	}
	if inc := IncrementalHealth([]model.Checkpoint{ck("COMPLETED", 1000, 0)}, "RocksDB"); inc.Known || inc.Summary() != "" {
		t.Fatal("pre-1.15 Flink (no checkpointed_size) must render nothing")
	}
}

func TestRetention(t *testing.T) {
	var cfg model.CheckpointConfig
	cfg.Externalization.DeleteOnCancellation = true // Flink 2.1 reports this even when disabled
	if txt, warn := Retention(cfg); !warn || !strings.Contains(txt, "none") {
		t.Fatalf("disabled externalization: %q %v", txt, warn)
	}
	cfg.Externalization.Enabled = true
	if txt, warn := Retention(cfg); !warn || !strings.Contains(txt, "DELETED on cancel") {
		t.Fatalf("delete-on-cancel: %q %v", txt, warn)
	}
	cfg.Externalization.DeleteOnCancellation = false
	if _, warn := Retention(cfg); warn {
		t.Fatal("retain-on-cancel is the safe setting")
	}
}

func TestFailureStreakAndTolerance(t *testing.T) {
	hist := []model.Checkpoint{
		{Status: "IN_PROGRESS"}, {Status: "FAILED"}, {Status: "COMPLETED", IsSavepoint: true},
		{Status: "FAILED"}, {Status: "COMPLETED"}, {Status: "FAILED"},
	}
	if got := FailureStreak(hist); got != 2 {
		t.Fatalf("streak = %d, want 2", got)
	}
	cases := []struct {
		tol, streak int
		level       string
	}{{0, 0, "ok"}, {3, 0, "ok"}, {3, 1, "warn"}, {3, 3, "crit"}, {0, 1, "crit"}}
	for _, c := range cases {
		if _, lvl := Tolerance(c.tol, c.streak); lvl != c.level {
			t.Errorf("Tolerance(%d,%d) = %s, want %s", c.tol, c.streak, lvl, c.level)
		}
	}
}
