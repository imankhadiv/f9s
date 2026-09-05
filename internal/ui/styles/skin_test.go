package styles

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skin.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSkinMissingFileKeepsDefaults(t *testing.T) {
	if err := LoadSkin(filepath.Join(t.TempDir(), "nope.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSkinHexAndAdaptive(t *testing.T) {
	err := LoadSkin(write(t, `
colors:
  accent: "#00FF00"
  ok: {light: "#004400", dark: "#88FF88"}
  warn: "214"
`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoadSkinUnknownTokenFailsLoudly(t *testing.T) {
	if err := LoadSkin(write(t, "colors:\n  acent: \"#fff\"\n")); err == nil {
		t.Fatal("typo'd token must error, not silently ignore")
	}
}
