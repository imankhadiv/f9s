package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugins.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	ps, _, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil || len(ps) != 0 {
		t.Fatalf("want empty, got %v / %v", ps, err)
	}
}

func TestLoadValidPlugin(t *testing.T) {
	ps, _, err := Load(write(t, `
plugins:
  explain:
    key: "e"
    description: "explain with AI"
    command: "sh"
    args: ["-c", "cat"]
    background: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Name != "explain" || ps[0].Key != "e" || !ps[0].Background {
		t.Fatalf("unexpected plugins: %+v", ps)
	}
}

func TestReservedKeyRejected(t *testing.T) {
	_, _, err := Load(write(t, `
plugins:
  bad: {key: "s", command: "sh"}
`))
	if err == nil {
		t.Fatal("want error for reserved key")
	}
}

func TestMissingCommandRejected(t *testing.T) {
	_, _, err := Load(write(t, `
plugins:
  bad: {key: "e"}
`))
	if err == nil {
		t.Fatal("want error for missing command")
	}
}
