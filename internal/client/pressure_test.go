package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

// VertexSubtaskPressure fans the 5 buffer/busy metrics across subtasks into the
// indexed slices, parsing the "<idx>.<metric>" ids.
func TestVertexSubtaskPressure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":"0.busyTimeMsPerSecond","value":"950"},
			{"id":"0.backPressuredTimeMsPerSecond","value":"40"},
			{"id":"0.idleTimeMsPerSecond","value":"10"},
			{"id":"0.inPoolUsage","value":"0.12"},
			{"id":"0.outPoolUsage","value":"0.98"},
			{"id":"1.busyTimeMsPerSecond","value":"120"},
			{"id":"1.inPoolUsage","value":"0.95"},
			{"id":"1.outPoolUsage","value":"0.05"}
		]`))
	}))
	defer srv.Close()

	sp, err := New(config.Context{URL: srv.URL}).VertexSubtaskPressure("j", "v", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(sp.Busy) != 2 || sp.Busy[0] != 950 || sp.Busy[1] != 120 {
		t.Errorf("busy: %v", sp.Busy)
	}
	if sp.OutPool[0] != 0.98 || sp.InPool[1] != 0.95 {
		t.Errorf("pools: in=%v out=%v", sp.InPool, sp.OutPool)
	}
	// A metric absent for subtask 1 (backpressured) stays zero, not a panic.
	if sp.Backpressured[1] != 0 {
		t.Errorf("missing metric should default to 0, got %v", sp.Backpressured[1])
	}
}

func TestVertexSubtaskPressureZeroParallelism(t *testing.T) {
	// No request should be needed; empty slices come back.
	sp, err := New(config.Context{URL: "http://unused"}).VertexSubtaskPressure("j", "v", 0)
	if err != nil || len(sp.Busy) != 0 {
		t.Errorf("par=0 should be a no-op, got err=%v busy=%v", err, sp.Busy)
	}
}
