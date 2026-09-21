package views

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/model"
)

func loadFixture(t *testing.T, name string, out any) {
	t.Helper()
	data, err := os.ReadFile("../client/testdata/flink-2.1/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}

// Real Flink 2.1 output: externalization disabled, tolerable 0, hashmap backend.
func TestCheckpointsViewShowsHealthFromRealFixture(t *testing.T) {
	var cp model.Checkpoints
	var cfg model.CheckpointConfig
	loadFixture(t, "checkpoints.json", &cp)
	loadFixture(t, "checkpoint-config.json", &cfg)
	if cp.History[0].CheckpointedSize == 0 {
		t.Fatal("fixture checkpointed_size did not decode")
	}

	v := NewCheckpoints(Deps{Ctx: "dev", Cfg: &config.Config{Contexts: map[string]config.Context{"dev": {}}}}, "j", "demo")
	v.Update(checkpointsMsg{cp: cp, cfg: cfg})
	body := v.View(200, 60)
	for _, want := range []string{"DELTA", "retention: none", "tolerates 0 consecutive", "incremental", "heap backend"} {
		if !strings.Contains(body, want) {
			t.Errorf("view missing %q:\n%s", want, body)
		}
	}
	ctx, _ := v.AIContext()
	if !strings.Contains(ctx, "retention: none") {
		t.Errorf("AI context lacks retention:\n%s", ctx)
	}
}
