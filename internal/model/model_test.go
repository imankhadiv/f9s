package model

import (
	"encoding/json"
	"testing"
)

func FuzzMetricFloat(f *testing.F) {
	f.Add(`{"id":"x","value":"12.5"}`)
	f.Add(`{"id":"x","min":1,"max":2,"avg":1.5,"sum":3}`)
	f.Add(`{"id":"x","value":"NaN"}`)
	f.Add(`{"id":"x","value":""}`)
	f.Fuzz(func(t *testing.T, data string) {
		var m Metric
		if json.Unmarshal([]byte(data), &m) != nil {
			return
		}
		m.Float() // must never panic, whatever the wire carried
	})
}

// Flink serializes NaN percentiles as the STRING "NaN" on young jobs —
// discovered by the fixture harness; must never fail the whole parse again.
func TestSummaryStatToleratesNaNStrings(t *testing.T) {
	var cp Checkpoints
	raw := `{"counts":{"total":1},"summary":{"state_size":{"min":0,"max":0,"avg":0,"p50":"NaN","p90":"NaN","p99":"NaN","p999":"NaN"},"end_to_end_duration":{"min":1,"max":5,"avg":2,"p50":2.0,"p90":4.0,"p99":5.0,"p999":5.0}}}`
	if err := json.Unmarshal([]byte(raw), &cp); err != nil {
		t.Fatalf("NaN strings must not fail the parse: %v", err)
	}
	if cp.Summary.StateSize.P50 != 0 || cp.Summary.EndToEndDuration.P90 != 4 {
		t.Fatalf("bad values: %+v", cp.Summary)
	}
}

func TestFmtDurationSubSecond(t *testing.T) {
	cases := map[int64]string{0: "0ms", 5: "5ms", 999: "999ms", 1000: "1s", 90_000: "1m30s"}
	for ms, want := range cases {
		if got := FmtDuration(ms); got != want {
			t.Errorf("FmtDuration(%d) = %q, want %q", ms, got, want)
		}
	}
}
