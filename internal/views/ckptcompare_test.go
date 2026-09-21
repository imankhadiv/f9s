package views

import (
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/model"
)

func TestCheckpointCompareHeadlinesTheGrower(t *testing.T) {
	c := NewCheckpointCompare(Deps{}, "j", "orders", []int64{3, 2, 1})
	c.Update(ckptCompareMsg{
		names: map[string]string{"agg": "Window Aggregate", "src": "Kafka Source"},
		details: []model.CheckpointDetail{
			{ID: 3, Tasks: map[string]model.CheckpointTaskStats{"agg": {StateSize: 4 << 20, EndToEndDuration: 9000}, "src": {StateSize: 1 << 10}}},
			{ID: 2, Tasks: map[string]model.CheckpointTaskStats{"agg": {StateSize: 2 << 20, EndToEndDuration: 3000}, "src": {StateSize: 1 << 10}}},
			{ID: 1, Tasks: map[string]model.CheckpointTaskStats{"agg": {StateSize: 1 << 20, EndToEndDuration: 1000}, "src": {StateSize: 1 << 10}}},
		},
	})
	body := c.View(160, 20)
	for _, want := range []string{"#1", "#3", "Window Aggregate grew the most in state size", "+300%", "4.0MiB"} {
		if !strings.Contains(body, want) {
			t.Errorf("compare view missing %q:\n%s", want, body)
		}
	}
	c.Update(keyMsg("m"))
	if body := c.View(160, 20); !strings.Contains(body, "metric: duration") || !strings.Contains(body, "+800%") {
		t.Errorf("m must switch to durations:\n%s", body)
	}
	if ctx, _ := c.AIContext(); !strings.Contains(ctx, "Window Aggregate") {
		t.Errorf("AI context:\n%s", ctx)
	}
}

func TestCheckpointsCompareKeyPicksCompletedFromCursor(t *testing.T) {
	v := NewCheckpoints(Deps{Ctx: "d", Cfg: &config.Config{Contexts: map[string]config.Context{"d": {}}}}, "j", "orders")
	v.Update(checkpointsMsg{cp: model.Checkpoints{History: []model.Checkpoint{
		{ID: 9, Status: "IN_PROGRESS"}, {ID: 8, Status: "COMPLETED"}, {ID: 7, Status: "FAILED"},
		{ID: 6, Status: "COMPLETED", IsSavepoint: true}, {ID: 5, Status: "COMPLETED"},
	}}})
	if got := v.compareIDs(); len(got) != 2 || got[0] != 8 || got[1] != 5 {
		t.Fatalf("compare ids = %v, want [8 5]", got)
	}
	_, cmd := v.Update(keyMsg("c"))
	if pm, ok := cmd().(PushMsg); !ok || pm.M.Title() != "ckpt compare" {
		t.Fatalf("c pushed %#v", cmd())
	}
}
