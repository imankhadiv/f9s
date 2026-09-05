package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditAppend(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	Append(Record{Context: "prod", Action: "cancel", Target: "job-1", Result: "ok", Source: "tui"})
	Append(Record{Context: "prod", Action: "savepoint", Target: "job-2", Result: "ok", Source: "mcp"})
	data, err := os.ReadFile(filepath.Join(dir, "f9s", "audit", "prod.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1
	if lines != 2 {
		t.Fatalf("want 2 audit lines, got %d", lines)
	}
	if !strings.Contains(string(data), `"action":"cancel"`) || !strings.Contains(string(data), `"source":"mcp"`) {
		t.Fatalf("audit content wrong:\n%s", data)
	}
	// records auto-fill a user
	if !strings.Contains(string(data), `"user":`) {
		t.Error("audit record missing auto-filled user")
	}
}
