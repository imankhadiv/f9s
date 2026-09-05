package views

import (
	"strings"
	"testing"
)

func TestWatermarkClassifier(t *testing.T) {
	cases := []struct {
		name string
		rows []wmRow
		want int    // stall index
		has  string // substring of the verdict
	}{
		{
			name: "healthy — everything current",
			rows: []wmRow{{num: 1, isSource: true, lagMs: 2000}, {num: 2, lagMs: 3000}},
			want: -1,
		},
		{
			name: "idle source holds watermark",
			rows: []wmRow{{num: 1, name: "kafka-src", isSource: true, lagMs: -1, idleMs: 240000}, {num: 2, lagMs: -1}},
			want: 0, has: "IDLE",
		},
		{
			name: "no watermark strategy",
			rows: []wmRow{{num: 1, name: "src", isSource: true, lagMs: -1, idleMs: -1}},
			want: 0, has: "no watermark yet",
		},
		{
			name: "lag jumps at an operator, upstream healthy",
			rows: []wmRow{{num: 1, isSource: true, lagMs: 3000}, {num: 2, name: "window", lagMs: 900000}},
			want: 1, has: "bounded-out-of-orderness",
		},
		{
			name: "one input subtask far behind",
			rows: []wmRow{{num: 1, isSource: true, lagMs: 3000}, {num: 2, name: "join", lagMs: 700000, spread: 650000}},
			want: 1, has: "input subtask lags",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stall, verdict := classifyWatermarks(c.rows)
			if stall != c.want {
				t.Fatalf("stall = %d, want %d (verdict %q)", stall, c.want, verdict)
			}
			if c.has != "" && !strings.Contains(verdict, c.has) {
				t.Fatalf("verdict %q missing %q", verdict, c.has)
			}
		})
	}
}
