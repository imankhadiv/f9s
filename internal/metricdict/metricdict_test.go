package metricdict

import "testing"

func TestLookup(t *testing.T) {
	// a known suffix should resolve; an unknown one should not
	if _, ok := Lookup("numRecordsInPerSecond"); !ok {
		t.Error("expected a known metric to resolve")
	}
	if _, ok := Lookup("totally.made.up.metric.xyz"); ok {
		t.Error("unknown metric should not resolve")
	}
}
