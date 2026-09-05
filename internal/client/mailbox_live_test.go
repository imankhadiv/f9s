package client

import (
	"os"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

// Live probe against the playground; skipped unless F9S_LIVE_TEST=1.
func TestVertexMailboxLive(t *testing.T) {
	if os.Getenv("F9S_LIVE_TEST") != "1" {
		t.Skip("live playground test")
	}
	c := New(config.Context{URL: "http://localhost:8081"})
	jobs, err := c.Jobs()
	if err != nil || len(jobs) == 0 {
		t.Skip("no live jobs")
	}
	job, err := c.Job(jobs[0].JID)
	if err != nil || len(job.Vertices) == 0 {
		t.Fatalf("job fetch: %v", err)
	}
	p99, soft, _, err := c.VertexMailbox(job.JID, job.Vertices[len(job.Vertices)-1].ID)
	if err != nil {
		t.Fatalf("mailbox: %v", err)
	}
	if p99 < 0 || soft < 0 {
		t.Fatalf("expected live mailbox values, got p99=%v soft=%v", p99, soft)
	}
	t.Logf("mailbox p99=%.1fms softBP=%.1fms/s", p99, soft)
}
