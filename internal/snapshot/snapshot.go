// Package snapshot keeps a lightweight per-job config journal: parallelism,
// checkpoint settings, restart strategy, cluster version. Appended only on
// change, it answers the first incident question — "did anything change?" —
// with receipts.
package snapshot

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Snap is one job-config observation.
type Snap struct {
	Time         time.Time      `json:"time"`
	JID          string         `json:"jid"`
	Name         string         `json:"name"`
	Parallelism  map[string]int `json:"parallelism"` // vertex name → parallelism
	CkptInterval int64          `json:"ckptInterval"`
	CkptTimeout  int64          `json:"ckptTimeout"`
	Unaligned    bool           `json:"unaligned"`
	Backend      string         `json:"backend"`
	Restart      string         `json:"restart"`
	FlinkVersion string         `json:"flinkVersion"`
}

func file(ctx string) string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join("snapshots", ctx+".jsonl")
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "f9s", "snapshots", ctx+".jsonl")
}

// Diff renders the human-readable changes from prev to cur; empty when
// nothing observable changed.
func Diff(prev, cur Snap) []string {
	var out []string
	keys := map[string]bool{}
	for k := range prev.Parallelism {
		keys[k] = true
	}
	for k := range cur.Parallelism {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		p, c := prev.Parallelism[k], cur.Parallelism[k]
		if p != c {
			short := k
			if len(short) > 40 {
				short = short[:39] + "…"
			}
			out = append(out, fmt.Sprintf("parallelism %q %d→%d", short, p, c))
		}
	}
	if prev.CkptInterval != cur.CkptInterval {
		out = append(out, fmt.Sprintf("checkpoint interval %dms→%dms", prev.CkptInterval, cur.CkptInterval))
	}
	if prev.CkptTimeout != cur.CkptTimeout {
		out = append(out, fmt.Sprintf("checkpoint timeout %dms→%dms", prev.CkptTimeout, cur.CkptTimeout))
	}
	if prev.Unaligned != cur.Unaligned {
		out = append(out, fmt.Sprintf("unaligned checkpoints %v→%v", prev.Unaligned, cur.Unaligned))
	}
	if prev.Backend != cur.Backend {
		out = append(out, fmt.Sprintf("state backend %s→%s", prev.Backend, cur.Backend))
	}
	if prev.Restart != cur.Restart {
		out = append(out, fmt.Sprintf("restart strategy %q→%q", prev.Restart, cur.Restart))
	}
	if prev.FlinkVersion != cur.FlinkVersion && prev.FlinkVersion != "" {
		out = append(out, fmt.Sprintf("flink %s→%s", prev.FlinkVersion, cur.FlinkVersion))
	}
	return out
}

// Load returns a job's journal, oldest first.
func Load(ctx, jid string) []Snap {
	f, err := os.Open(file(ctx))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Snap
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 256*1024)
	for sc.Scan() {
		var s Snap
		if json.Unmarshal(sc.Bytes(), &s) == nil && (jid == "" || s.JID == jid) {
			out = append(out, s)
		}
	}
	return out
}

// last returns the newest snapshot per jid.
func last(ctx string) map[string]Snap {
	out := map[string]Snap{}
	for _, s := range Load(ctx, "") {
		out[s.JID] = s
	}
	return out
}

// AppendIfChanged persists cur when it differs from the job's last snapshot
// (or is the first sighting), returning the diffs.
func AppendIfChanged(ctx string, cur Snap) (bool, []string) {
	prevAll := last(ctx)
	prev, seen := prevAll[cur.JID]
	var diffs []string
	if seen {
		diffs = Diff(prev, cur)
		if len(diffs) == 0 {
			return false, nil
		}
	} else {
		diffs = []string{"first observed"}
	}
	path := file(ctx)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false, nil
	}
	defer f.Close()
	data, err := json.Marshal(cur)
	if err != nil {
		return false, nil
	}
	f.Write(append(data, '\n'))
	return true, diffs
}
