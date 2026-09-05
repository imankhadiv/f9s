// Package watch stores the results of alert-triggered background
// investigations (v2 watch mode): queued findings the operator reads when
// ready — never a modal interrupt.
package watch

import (
	"sync"
	"time"
)

// Result is one completed background investigation.
type Result struct {
	StartedAt time.Time
	Job       string
	JID       string
	Trigger   string // alert kind that fired it
	Steps     []string
	Answer    string
	Err       string
}

const keepResults = 20

// Store holds recent results plus the dedup/budget bookkeeping.
type Store struct {
	mu      sync.Mutex
	results []Result
	recent  map[string]time.Time // fingerprint -> last investigation
	started []time.Time          // for the hourly budget
}

// NewStore builds an empty store.
func NewStore() *Store {
	return &Store{recent: map[string]time.Time{}}
}

// ShouldInvestigate applies the cooldown (30m per job+kind) and budget
// (4/hour) rules; when it returns true the attempt is recorded.
func (s *Store) ShouldInvestigate(fingerprint string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.recent[fingerprint]; ok && now.Sub(last) < 30*time.Minute {
		return false
	}
	live := s.started[:0]
	for _, t := range s.started {
		if now.Sub(t) < time.Hour {
			live = append(live, t)
		}
	}
	s.started = live
	if len(s.started) >= 4 {
		return false
	}
	s.recent[fingerprint] = now
	s.started = append(s.started, now)
	return true
}

// Add records a completed investigation.
// PruneRecent drops fingerprint-cooldown entries older than 2h — they can
// no longer influence dedup and only accumulate on churny clusters.
func (s *Store) PruneRecent(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for fp, t := range s.recent {
		if now.Sub(t) > 2*time.Hour {
			delete(s.recent, fp)
		}
	}
}

func (s *Store) Add(r Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, r)
	if len(s.results) > keepResults {
		s.results = s.results[len(s.results)-keepResults:]
	}
}

// List returns results newest first.
func (s *Store) List() []Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Result, 0, len(s.results))
	for i := len(s.results) - 1; i >= 0; i-- {
		out = append(out, s.results[i])
	}
	return out
}
