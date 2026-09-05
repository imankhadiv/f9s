package tools

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
)

func TestSignalHistoryLive(t *testing.T) {
	if os.Getenv("F9S_LIVE_TEST") != "1" {
		t.Skip("live playground test")
	}
	cl := client.New(config.Context{URL: "http://localhost:8081"})
	jobs, err := cl.Jobs()
	if err != nil {
		t.Skip("no cluster")
	}
	jid := ""
	for _, j := range jobs {
		if j.State == "RUNNING" && !strings.Contains(j.Name, "chaos") {
			jid = j.JID
			break
		}
	}
	if jid == "" {
		t.Skip("no running non-chaos job")
	}
	reg := New(cl, "test")
	tool, ok := reg.Get("get_signal_history")
	if !ok {
		t.Fatal("tool missing")
	}
	out, err := tool.Run(context.Background(), map[string]string{"jid": jid})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"checkpoints_newest_first", "duration_ms", "num_restarts"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out[:min(400, len(out))])
		}
	}
	t.Logf("series ok: %s…", out[:120])
}
