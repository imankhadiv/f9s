// Package sentinel is f9s's continuous backpressure watchdog: it keeps a
// ring buffer of per-vertex samples and raises/clears alerts with hysteresis
// so flapping never spams the operator.
package sentinel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// BufferLen is ~1h of history at the 15s sample interval.
	BufferLen = 240
	// fireAfter consecutive bad samples open an alert; clearAfter
	// consecutive good samples close it (hysteresis).
	fireAfter  = 3
	clearAfter = 3
)

// Key identifies a sampled vertex.
type Key struct {
	JID string
	VID string
}

// Sample is one observation of a vertex.
type Sample struct {
	Time   time.Time
	Level  string  // ok / low / high
	BusyMs float64 // busyTimeMsPerSecond; -1 unknown
}

// Alert is one episode (open until ClosedAt is set).
type Alert struct {
	Key      Key
	Job      string
	Vertex   string
	Kind     string // "backpressure"
	OpenedAt time.Time
	ClosedAt time.Time // zero while open
	Peak     string    // worst level seen
	Samples  int       // observations while open
	// AtFire is the frozen per-subtask distribution snapshot from the
	// moment the alert opened (skew alerts; nil otherwise).
	AtFire []float64
}

// Open reports whether the episode is still firing.
func (a Alert) Open() bool { return a.ClosedAt.IsZero() }

type vertexState struct {
	badStreak  int
	goodStreak int
	open       *Alert
}

// skew thresholds: two-threshold hysteresis (fire high, clear lower) so a
// score hovering between them neither fires nor clears.
const (
	skewFireAt  = 50
	skewClearAt = 35
)

type skewState struct {
	scores      []int // recent scores for the trend arrow
	fireStreak  int
	clearStreak int
	peak        int
	open        *Alert
}

// Sentinel holds sample history and the alert engine. Safe for concurrent
// use (samples arrive from tea.Cmd goroutines).
type Sentinel struct {
	mu       sync.Mutex
	path     string // JSONL persistence; empty disables
	buf      map[Key][]Sample
	state    map[Key]*vertexState
	skew     map[Key]*skewState
	episodes []Alert // closed episodes, newest last
}

// New builds an empty sentinel.
func New() *Sentinel {
	return &Sentinel{buf: map[Key][]Sample{}, state: map[Key]*vertexState{}, skew: map[Key]*skewState{}}
}

const persistMaxBytes = 8 << 20

// persistRecord is one JSONL line of sample history.
type persistRecord struct {
	T     time.Time `json:"t"`
	JID   string    `json:"jid"`
	VID   string    `json:"vid"`
	Level string    `json:"level"`
	Busy  float64   `json:"busy"`
}

// dataPath is ~/.local/share/f9s/bp/<context>.jsonl.
func dataPath(contextName string) string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join("bp", contextName+".jsonl")
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "f9s", "bp", contextName+".jsonl")
}

// NewPersistent builds a sentinel that appends samples to the context's
// history file and preloads the ring buffers from it, so sparklines survive
// restarts. The file rotates (truncating to fresh) past 8MiB.
func NewPersistent(contextName string) *Sentinel {
	s := New()
	s.path = dataPath(contextName)
	if data, err := os.ReadFile(s.path); err == nil && len(data) <= persistMaxBytes {
		for _, line := range bytes.Split(data, []byte("\n")) {
			var r persistRecord
			if json.Unmarshal(line, &r) != nil {
				continue
			}
			k := Key{JID: r.JID, VID: r.VID}
			buf := append(s.buf[k], Sample{Time: r.T, Level: r.Level, BusyMs: r.Busy})
			if len(buf) > BufferLen {
				buf = buf[len(buf)-BufferLen:]
			}
			s.buf[k] = buf
		}
	}
	return s
}

// persist appends one sample line, rotating past the size cap.
func (s *Sentinel) persist(k Key, sample Sample) {
	if s.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	if fi, err := os.Stat(s.path); err == nil && fi.Size() > persistMaxBytes {
		_ = os.Truncate(s.path, 0)
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	data, err := json.Marshal(persistRecord{T: sample.Time, JID: k.JID, VID: k.VID, Level: sample.Level, Busy: sample.BusyMs})
	if err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
}

func bad(level string) bool { return level == "high" }

func known(level string) bool {
	return level == "ok" || level == "low" || level == "high"
}

// Observe records one sample and returns the alert opened or closed by it
// (nil, nil for no transition).
func (s *Sentinel) Observe(k Key, job, vertex string, sample Sample) (opened, closed *Alert) {
	s.mu.Lock()
	defer s.mu.Unlock()

	buf := append(s.buf[k], sample)
	if len(buf) > BufferLen {
		buf = buf[len(buf)-BufferLen:]
	}
	s.buf[k] = buf
	s.persist(k, sample)

	if !known(sample.Level) {
		return nil, nil
	}

	st := s.state[k]
	if st == nil {
		st = &vertexState{}
		s.state[k] = st
	}

	if bad(sample.Level) {
		st.badStreak++
		st.goodStreak = 0
	} else {
		st.goodStreak++
		st.badStreak = 0
	}

	if st.open != nil {
		st.open.Samples++
		if bad(sample.Level) {
			st.open.Peak = "high"
		}
		if st.goodStreak >= clearAfter {
			st.open.ClosedAt = sample.Time
			done := *st.open
			s.episodes = append(s.episodes, done)
			s.capEpisodes()
			st.open = nil
			return nil, &done
		}
		return nil, nil
	}

	if st.badStreak >= fireAfter {
		st.open = &Alert{
			Key: k, Job: job, Vertex: vertex, Kind: "backpressure",
			OpenedAt: sample.Time, Peak: sample.Level, Samples: st.badStreak,
		}
		a := *st.open
		return &a, nil
	}
	return nil, nil
}

// ObserveCondition is the generic boolean alert channel (checkpoint-failure
// streaks, restart loops): fireAfter consecutive bad observations open an
// alert of the given kind, clearAfter good ones close it.
func (s *Sentinel) ObserveCondition(k Key, kind, job, vertex string, isBad bool, t time.Time) (opened, closed *Alert) {
	return s.ObserveConditionKnown(k, kind, job, vertex, isBad, true, t)
}

// ObserveConditionKnown observes a boolean condition only when its telemetry
// is authoritative. Unknown samples pause both fire and recovery streaks.
func (s *Sentinel) ObserveConditionKnown(k Key, kind, job, vertex string, isBad, isKnown bool, t time.Time) (opened, closed *Alert) {
	if !isKnown {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ck := k
	ck.VID = ck.VID + "|" + kind // separate state per kind
	st := s.state[ck]
	if st == nil {
		st = &vertexState{}
		s.state[ck] = st
	}
	if isBad {
		st.badStreak++
		st.goodStreak = 0
	} else {
		st.goodStreak++
		st.badStreak = 0
	}
	if st.open != nil {
		st.open.Samples++
		if st.goodStreak >= clearAfter {
			st.open.ClosedAt = t
			done := *st.open
			s.episodes = append(s.episodes, done)
			s.capEpisodes()
			st.open = nil
			return nil, &done
		}
		return nil, nil
	}
	if st.badStreak >= fireAfter {
		st.open = &Alert{Key: k, Job: job, Vertex: vertex, Kind: kind, OpenedAt: t, Peak: kind, Samples: st.badStreak}
		a := *st.open
		return &a, nil
	}
	return nil, nil
}

// ObserveSkew records one skew score (0-100) and returns the alert opened
// or closed by it. Fires at ≥50 sustained, clears below 35 sustained;
// scores between the thresholds hold the current state.
func (s *Sentinel) ObserveSkew(k Key, job, vertex string, score int, values []float64, t time.Time) (opened, closed *Alert) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.skew[k]
	if st == nil {
		st = &skewState{}
		s.skew[k] = st
	}
	st.scores = append(st.scores, score)
	if len(st.scores) > 20 {
		st.scores = st.scores[len(st.scores)-20:]
	}
	switch {
	case score >= skewFireAt:
		st.fireStreak++
		st.clearStreak = 0
	case score < skewClearAt:
		st.clearStreak++
		st.fireStreak = 0
	default: // hysteresis band: hold
		st.fireStreak = 0
		st.clearStreak = 0
	}
	if score > st.peak {
		st.peak = score
	}
	if st.open != nil {
		st.open.Samples++
		st.open.Peak = fmt.Sprintf("%d%%", st.peak)
		if st.clearStreak >= clearAfter {
			st.open.ClosedAt = t
			done := *st.open
			s.episodes = append(s.episodes, done)
			s.capEpisodes()
			st.open = nil
			st.peak = 0
			return nil, &done
		}
		return nil, nil
	}
	if st.fireStreak >= fireAfter {
		st.open = &Alert{
			Key: k, Job: job, Vertex: vertex, Kind: "skew",
			OpenedAt: t, Peak: fmt.Sprintf("%d%%", score), Samples: st.fireStreak,
			AtFire: append([]float64(nil), values...),
		}
		st.peak = score
		a := *st.open
		return &a, nil
	}
	return nil, nil
}

// SkewTrend compares the recent skew scores against the window before them:
// "rising" / "falling" on a sustained ≥8-point move, "stable" otherwise,
// "" with fewer than 6 samples.
func (s *Sentinel) SkewTrend(k Key) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.skew[k]
	if st == nil || len(st.scores) < 6 {
		return ""
	}
	n := len(st.scores)
	half := 3
	recent, before := 0.0, 0.0
	for _, v := range st.scores[n-half:] {
		recent += float64(v)
	}
	for _, v := range st.scores[n-2*half : n-half] {
		before += float64(v)
	}
	recent /= float64(half)
	before /= float64(half)
	switch {
	case recent-before >= 8:
		return "rising"
	case before-recent >= 8:
		return "falling"
	}
	return "stable"
}

// Prune drops per-vertex state for jobs no longer alive, so multi-day
// sessions on churny clusters don't leak. Closed episodes are kept (they
// are the history) but capped elsewhere.
func (s *Sentinel) Prune(liveJIDs map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.buf {
		if !liveJIDs[k.JID] {
			delete(s.buf, k)
		}
	}
	for k := range s.state {
		if !liveJIDs[k.JID] {
			delete(s.state, k)
		}
	}
	for k := range s.skew {
		if !liveJIDs[k.JID] {
			delete(s.skew, k)
		}
	}
}

const maxEpisodes = 200

func (s *Sentinel) capEpisodes() {
	if len(s.episodes) > maxEpisodes {
		s.episodes = s.episodes[len(s.episodes)-maxEpisodes:]
	}
}

// OpenAlerts returns currently-firing alerts.
func (s *Sentinel) OpenAlerts() []Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Alert
	for _, st := range s.state {
		if st.open != nil {
			out = append(out, *st.open)
		}
	}
	for _, st := range s.skew {
		if st.open != nil {
			out = append(out, *st.open)
		}
	}
	return out
}

// Episodes returns closed episodes, newest first.
func (s *Sentinel) Episodes() []Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Alert, 0, len(s.episodes))
	for i := len(s.episodes) - 1; i >= 0; i-- {
		out = append(out, s.episodes[i])
	}
	return out
}

// History returns a copy of a vertex's sample buffer.
func (s *Sentinel) History(k Key) []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Sample, len(s.buf[k]))
	copy(out, s.buf[k])
	return out
}
