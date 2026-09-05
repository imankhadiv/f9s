package ledger

import (
	"testing"
	"time"
)

func TestAppendAndList(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	if entries, err := List("prod"); err != nil || len(entries) != 0 {
		t.Fatalf("empty ledger: entries=%v err=%v", entries, err)
	}

	first := Entry{Time: time.Now().Add(-time.Hour), JID: "j1", Job: "orders", Action: "savepoint", Path: "s3://b/sp-1"}
	second := Entry{Time: time.Now(), JID: "j1", Job: "orders", Action: "stop", Path: "s3://b/sp-2"}
	for _, e := range []Entry{first, second} {
		if err := Append("prod", e); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := List("prod")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].Path != "s3://b/sp-2" {
		t.Fatalf("want newest first, got %+v", entries[0])
	}

	// contexts are isolated
	if other, _ := List("staging"); len(other) != 0 {
		t.Fatalf("staging should be empty, got %v", other)
	}
}
