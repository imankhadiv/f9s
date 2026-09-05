package analyze

import "testing"

func TestNoBackpressureNoBottleneck(t *testing.T) {
	vs := []VertexSample{
		{Name: "source", Level: "ok", BusyMsPerSec: 100},
		{Name: "sink", Level: "ok", BusyMsPerSec: 200},
	}
	if got := Bottleneck(vs); got != -1 {
		t.Fatalf("want -1, got %d", got)
	}
}

func TestBottleneckIsBusiestDownstreamOfBackpressure(t *testing.T) {
	vs := []VertexSample{
		{Name: "source", Level: "high", BusyMsPerSec: 50},
		{Name: "map", Level: "low", BusyMsPerSec: 300},
		{Name: "window", Level: "ok", BusyMsPerSec: 950}, // the culprit
		{Name: "sink", Level: "ok", BusyMsPerSec: 100},
	}
	if got := Bottleneck(vs); got != 2 {
		t.Fatalf("want 2 (window), got %d", got)
	}
}

func TestBottleneckWithoutBusyMetricsPicksFirstDownstream(t *testing.T) {
	vs := []VertexSample{
		{Name: "source", Level: "high", BusyMsPerSec: -1},
		{Name: "map", Level: "ok", BusyMsPerSec: -1}, // first non-backpressured
		{Name: "sink", Level: "ok", BusyMsPerSec: -1},
	}
	if got := Bottleneck(vs); got != 1 {
		t.Fatalf("want 1 (map), got %d", got)
	}
}

func TestBackpressureReachingSinkBlamesLastBackpressured(t *testing.T) {
	vs := []VertexSample{
		{Name: "source", Level: "low", BusyMsPerSec: 200},
		{Name: "sink", Level: "high", BusyMsPerSec: 900},
	}
	if got := Bottleneck(vs); got != 1 {
		t.Fatalf("want 1 (sink), got %d", got)
	}
}
