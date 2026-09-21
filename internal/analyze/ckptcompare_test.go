package analyze

import (
	"testing"

	"github.com/imankhadiv/f9s/internal/model"
)

func TestCompareCheckpoints(t *testing.T) {
	d := func(id int64, tasks map[string]model.CheckpointTaskStats) model.CheckpointDetail {
		return model.CheckpointDetail{ID: id, Tasks: tasks}
	}
	details := []model.CheckpointDetail{ // newest first
		d(3, map[string]model.CheckpointTaskStats{"agg": {StateSize: 400, EndToEndDuration: 900}, "src": {StateSize: 10, EndToEndDuration: 50}}),
		d(2, map[string]model.CheckpointTaskStats{"agg": {StateSize: 200, EndToEndDuration: 300}, "src": {StateSize: 10, EndToEndDuration: 40}}),
		d(1, map[string]model.CheckpointTaskStats{"agg": {StateSize: 100, EndToEndDuration: 100}, "src": {StateSize: 10, EndToEndDuration: 60},
			"old": {StateSize: 5}}),
	}
	got := CompareCheckpoints(details, CkptStateSize)
	if len(got) != 3 || got[0].VID != "agg" {
		t.Fatalf("biggest grower first: %+v", got)
	}
	if got[0].Values[0] != 100 || got[0].Values[2] != 400 || got[0].Change != 3 || got[0].Delta != 300 {
		t.Fatalf("agg trend = %+v", got[0])
	}
	for _, tr := range got {
		if tr.VID == "old" && (tr.Values[2] != -1 || tr.Change != 0) {
			t.Fatalf("a vertex missing from newer checkpoints must show -1, no change: %+v", tr)
		}
	}
	dur := CompareCheckpoints(details, CkptDuration)
	if dur[0].VID != "agg" || dur[0].Values[2] != 900 {
		t.Fatalf("duration comparison = %+v", dur[0])
	}
	if CkptAlignment.Next() != CkptStateSize || CkptDuration.IsBytes() {
		t.Fatal("metric cycling / units wrong")
	}
}
