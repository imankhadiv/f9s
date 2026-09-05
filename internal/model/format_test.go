package model

import "testing"

func TestFormatters(t *testing.T) {
	if got := FmtBytes(0); got == "" {
		t.Error("FmtBytes(0) empty")
	}
	if got := FmtBytes(1536); got == "" || got == "1536" {
		t.Errorf("FmtBytes(1536) not humanized: %q", got)
	}
	if FmtDuration(0) == "" || FmtDuration(1500) == "" || FmtDuration(3600000) == "" {
		t.Error("FmtDuration produced empty")
	}
	if ShortJID("1234567890abcdef") == "1234567890abcdef" {
		t.Error("ShortJID did not shorten")
	}
	if ShortJID("abc") != "abc" {
		t.Error("ShortJID mangled a short id")
	}
	// Metric.Float: plain value, aggregate avg, and absent
	if v, ok := (Metric{Value: "12.5"}).Float(); !ok || v != 12.5 {
		t.Errorf("Float(value) = %v %v", v, ok)
	}
	avg := 3.0
	if v, ok := (Metric{Avg: &avg}).Float(); !ok || v != 3.0 {
		t.Errorf("Float(avg) = %v %v", v, ok)
	}
	if _, ok := (Metric{}).Float(); ok {
		t.Error("empty Metric.Float should be !ok")
	}
}
