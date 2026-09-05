package watch

import (
	"testing"
	"time"
)

func TestCooldownAndBudget(t *testing.T) {
	s := NewStore()
	now := time.Now()
	if !s.ShouldInvestigate("j1|skew", now) {
		t.Fatal("first investigation must be allowed")
	}
	if s.ShouldInvestigate("j1|skew", now.Add(time.Minute)) {
		t.Fatal("same fingerprint within 30m must be blocked")
	}
	if !s.ShouldInvestigate("j1|backpressure", now.Add(time.Minute)) {
		t.Fatal("different kind is a different fingerprint")
	}
	// budget: 4/hour total (2 used)
	if !s.ShouldInvestigate("j2|skew", now.Add(2*time.Minute)) || !s.ShouldInvestigate("j3|skew", now.Add(3*time.Minute)) {
		t.Fatal("3rd and 4th within budget must pass")
	}
	if s.ShouldInvestigate("j4|skew", now.Add(4*time.Minute)) {
		t.Fatal("5th within an hour must hit the budget")
	}
	if !s.ShouldInvestigate("j4|skew", now.Add(2*time.Hour)) {
		t.Fatal("budget must free up after an hour")
	}
}
