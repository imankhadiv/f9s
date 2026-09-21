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

func TestLongestSuffixWins(t *testing.T) {
	cases := map[string]string{
		"Status.JVM.CPU.Time": "cpu",
		"Status.JVM.GarbageCollector.G1_Young_Generation.Time": "gc",
		"Status.JVM.Memory.Metaspace.Used":                     "memory",
		"0.Source__Kafka.KafkaSourceReader.commitsFailed":      "kafka",
		"0.Window.rocksdb.compaction-pending":                  "state",
		"0.Map.softBackPressuredTimeMsPerSecond":               "backpressure",
		"0.Map.numRecordsInPerSecond":                          "throughput",
		"Status.Shuffle.Netty.AvailableMemorySegments":         "network",
	}
	for id, want := range cases {
		info, ok := Lookup(id)
		if !ok || info.Category != want {
			t.Errorf("Lookup(%q) = %+v %v, want category %s", id, info, ok, want)
		}
	}
}

func TestEntriesAreUniqueAndComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.suffix] {
			t.Errorf("duplicate suffix %q", e.suffix)
		}
		seen[e.suffix] = true
		if e.info.Category == "" || e.info.Unit == "" || e.info.Desc == "" {
			t.Errorf("%q is missing category/unit/desc", e.suffix)
		}
	}
	if len(entries) < 100 {
		t.Errorf("dictionary shrank to %d entries", len(entries))
	}
}
