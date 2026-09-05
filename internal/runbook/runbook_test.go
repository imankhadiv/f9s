package runbook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMatchInject(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "oom.md"), []byte(`---
job: "payments-*"
exception: "OutOfMemory"
---
Check TM heap headroom before suggesting rescale.`), 0o644)
	os.WriteFile(filepath.Join(dir, "all.md"), []byte("Always check checkpoint health first."), 0o644)

	books := Load(dir)
	if len(books) != 2 {
		t.Fatalf("want 2 runbooks, got %d", len(books))
	}
	m := Match(books, "payments-enricher", "java.lang.OutOfMemoryError: heap", "")
	if len(m) != 2 {
		t.Fatalf("both should match, got %d", len(m))
	}
	m = Match(books, "other-job", "clean evidence", "")
	if len(m) != 1 || m[0].Name != "all" {
		t.Fatalf("only the catch-all should match, got %+v", m)
	}
}
