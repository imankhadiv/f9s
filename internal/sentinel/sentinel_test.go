package sentinel

import (
	"fmt"
	"testing"
	"time"
)

func obs(s *Sentinel, level string, n int) (opened, closed *Alert) {
	k := Key{JID: "j1", VID: "v1"}
	for i := 0; i < n; i++ {
		o, c := s.Observe(k, "job", "vertex", Sample{Time: time.Now(), Level: level})
		if o != nil {
			opened = o
		}
		if c != nil {
			closed = c
		}
	}
	return
}

func TestAlertOpensAfterThreeBadSamples(t *testing.T) {
	s := New()
	if o, _ := obs(s, "high", 2); o != nil {
		t.Fatal("must not open before 3 consecutive bad samples")
	}
	o, _ := obs(s, "high", 1)
	if o == nil {
		t.Fatal("must open on the 3rd consecutive bad sample")
	}
	if len(s.OpenAlerts()) != 1 {
		t.Fatalf("want 1 open alert, got %d", len(s.OpenAlerts()))
	}
}

func TestFlappingDoesNotOpen(t *testing.T) {
	s := New()
	for i := 0; i < 5; i++ {
		obs(s, "high", 2)
		obs(s, "ok", 1) // resets the bad streak
	}
	if len(s.OpenAlerts()) != 0 {
		t.Fatal("flapping below the threshold must not alert")
	}
}

func TestAlertClosesWithHysteresisAndRecordsEpisode(t *testing.T) {
	s := New()
	obs(s, "high", 3) // open
	if _, c := obs(s, "ok", 2); c != nil {
		t.Fatal("must not close before 3 consecutive good samples")
	}
	_, c := obs(s, "ok", 1)
	if c == nil {
		t.Fatal("must close on the 3rd consecutive good sample")
	}
	if c.Open() {
		t.Fatal("closed episode must have ClosedAt set")
	}
	if len(s.OpenAlerts()) != 0 || len(s.Episodes()) != 1 {
		t.Fatalf("want 0 open / 1 episode, got %d / %d", len(s.OpenAlerts()), len(s.Episodes()))
	}
}

func TestUnknownBackpressureDoesNotClearOpenAlert(t *testing.T) {
	s := New()
	obs(s, "high", 3)
	if len(s.OpenAlerts()) != 1 {
		t.Fatal("expected an open alert")
	}
	if _, closed := obs(s, "", 10); closed != nil {
		t.Fatal("unknown samples must not clear an alert")
	}
	if len(s.OpenAlerts()) != 1 {
		t.Fatal("unknown samples removed the open alert")
	}
	if _, closed := obs(s, "ok", 3); closed == nil {
		t.Fatal("known recovery must still close the alert")
	}
}

func TestUnknownConditionDoesNotClearOpenAlert(t *testing.T) {
	s := New()
	k := Key{JID: "j1", VID: "job"}
	now := time.Now()
	for range [3]int{} {
		s.ObserveConditionKnown(k, "checkpoint", "job", "checkpointing", true, true, now)
	}
	for range [10]int{} {
		if _, closed := s.ObserveConditionKnown(k, "checkpoint", "job", "checkpointing", false, false, now); closed != nil {
			t.Fatal("unknown conditions must not clear an alert")
		}
	}
	if len(s.OpenAlerts()) != 1 {
		t.Fatal("unknown conditions removed the open alert")
	}
	var closed *Alert
	for range [3]int{} {
		_, closed = s.ObserveConditionKnown(k, "checkpoint", "job", "checkpointing", false, true, now)
	}
	if closed == nil {
		t.Fatal("known healthy conditions must close the alert")
	}
}

func TestHistoryRingCaps(t *testing.T) {
	s := New()
	k := Key{JID: "j1", VID: "v1"}
	for i := 0; i < BufferLen+50; i++ {
		s.Observe(k, "job", "vertex", Sample{Time: time.Now(), Level: "ok"})
	}
	if got := len(s.History(k)); got != BufferLen {
		t.Fatalf("ring must cap at %d, got %d", BufferLen, got)
	}
}

func TestSkewAlertHysteresis(t *testing.T) {
	s := New()
	k := Key{JID: "j1", VID: "v1"}
	now := time.Now()
	var opened *Alert
	for i := 0; i < 3; i++ {
		o, _ := s.ObserveSkew(k, "job", "vertex", 80, []float64{900, 30, 30, 30}, now)
		if o != nil {
			opened = o
		}
	}
	if opened == nil || opened.Kind != "skew" {
		t.Fatalf("3 samples ≥50 must open a skew alert, got %+v", opened)
	}
	// hysteresis band (35-50) holds the alert open
	for i := 0; i < 5; i++ {
		if _, c := s.ObserveSkew(k, "job", "vertex", 40, []float64{900, 30, 30, 30}, now); c != nil {
			t.Fatal("scores in the hysteresis band must not clear")
		}
	}
	// three low samples clear it
	var closed *Alert
	for i := 0; i < 3; i++ {
		_, c := s.ObserveSkew(k, "job", "vertex", 10, []float64{900, 30, 30, 30}, now)
		if c != nil {
			closed = c
		}
	}
	if closed == nil {
		t.Fatal("3 samples <35 must clear the skew alert")
	}
}

func TestObserveConditionKinds(t *testing.T) {
	s := New()
	k := Key{JID: "j1", VID: "job"}
	now := time.Now()
	var opened *Alert
	for i := 0; i < 3; i++ {
		o, _ := s.ObserveCondition(k, "checkpoint", "job", "checkpointing", true, now)
		if o != nil {
			opened = o
		}
	}
	if opened == nil || opened.Kind != "checkpoint" {
		t.Fatalf("3 bad conditions must open, got %+v", opened)
	}
	// a different kind on the same key is independent state
	if o, _ := s.ObserveCondition(k, "restart-loop", "job", "job", true, now); o != nil {
		t.Fatal("different kind must not inherit the streak")
	}
	var closed *Alert
	for i := 0; i < 3; i++ {
		_, c := s.ObserveCondition(k, "checkpoint", "job", "checkpointing", false, now)
		if c != nil {
			closed = c
		}
	}
	if closed == nil {
		t.Fatal("3 good conditions must close")
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	s1 := NewPersistent("testctx")
	k := Key{JID: "j1", VID: "v1"}
	for i := 0; i < 5; i++ {
		s1.Observe(k, "job", "vertex", Sample{Time: time.Now(), Level: "ok", BusyMs: float64(i * 100)})
	}
	s2 := NewPersistent("testctx")
	got := s2.History(k)
	if len(got) != 5 || got[4].BusyMs != 400 {
		t.Fatalf("history must survive restart, got %d samples %+v", len(got), got)
	}
}

func TestPruneDropsDeadJobs(t *testing.T) {
	s := New()
	now := time.Now()
	for _, jid := range []string{"live", "dead"} {
		k := Key{JID: jid, VID: "v"}
		s.Observe(k, "job", "vertex", Sample{Time: now, Level: "ok", BusyMs: 1})
		s.ObserveSkew(k, "job", "vertex", 10, nil, now)
	}
	s.Prune(map[string]bool{"live": true})
	if len(s.History(Key{JID: "dead", VID: "v"})) != 0 {
		t.Fatal("dead job history must be evicted")
	}
	if len(s.History(Key{JID: "live", VID: "v"})) != 1 {
		t.Fatal("live job history must survive")
	}
}

func TestEpisodeCap(t *testing.T) {
	s := New()
	now := time.Now()
	for i := 0; i < 250; i++ {
		k := Key{JID: fmt.Sprintf("j%d", i), VID: "v"}
		for range [3]int{} {
			s.ObserveCondition(k, "checkpoint", "job", "v", true, now)
		}
		for range [3]int{} {
			s.ObserveCondition(k, "checkpoint", "job", "v", false, now)
		}
	}
	if n := len(s.Episodes()); n > 200 {
		t.Fatalf("episodes unbounded: %d", n)
	}
}
