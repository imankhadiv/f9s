package metricts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// DataPath is ~/.local/share/f9s/metrics/<context>.json (XDG_DATA_HOME aware).
func DataPath(contextName string) string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join("metrics", contextName+".json")
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "f9s", "metrics", contextName+".json")
}

type savedPoint struct {
	T int64   `json:"t"` // unix ms
	V float64 `json:"v"`
}

type savedSeries struct {
	Scope  string       `json:"scope"`
	ID     string       `json:"id"`
	Metric string       `json:"metric"`
	Points []savedPoint `json:"points"`
}

type savedRing struct {
	Version int           `json:"version"`
	Series  []savedSeries `json:"series"`
}

// Save writes the ring as a snapshot (atomically: temp file + rename). The
// per-subtask distributions are not persisted, which keeps the file bounded
// by keys × ring size whatever the job parallelism.
func (r *Ring) Save(path string) error {
	if r == nil {
		return nil
	}
	var out savedRing
	out.Version = 1
	for _, k := range r.Keys("") {
		pts := r.Series(k)
		s := savedSeries{Scope: k.Scope, ID: k.ID, Metric: k.Metric, Points: make([]savedPoint, len(pts))}
		for i, p := range pts {
			s.Points[i] = savedPoint{T: p.Time.UnixMilli(), V: p.Value}
		}
		out.Series = append(out.Series, s)
	}
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".metrics-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Load merges a saved snapshot into the ring, dropping points older than
// maxAge (history that stale would draw misleading trends). A missing or
// unreadable file is not an error worth surfacing: the ring starts empty.
func (r *Ring) Load(path string, maxAge time.Duration, now time.Time) {
	if r == nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var in savedRing
	if json.Unmarshal(data, &in) != nil || in.Version != 1 {
		return
	}
	cutoff := now.Add(-maxAge)
	for _, s := range in.Series {
		k := Key{Scope: s.Scope, ID: s.ID, Metric: s.Metric}
		for _, p := range s.Points {
			t := time.UnixMilli(p.T)
			if t.Before(cutoff) || t.After(now.Add(time.Minute)) {
				continue
			}
			r.Add(k, Point{Time: t, Value: p.V})
		}
	}
}
