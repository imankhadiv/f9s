package config

import (
	"bytes"
	"os"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

var readmeConfigBlock = regexp.MustCompile("(?s)```yaml\\r?\\n(# ~/.config/f9s/config\\.yaml.*?)```")

// The README's configuration block is meant to be copy-pasted, so it has to
// parse into the real Config with no keys Config doesn't have — a documented
// setting that silently does nothing is worse than an undocumented one.
func TestReadmeConfigBlockParses(t *testing.T) {
	src, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	m := readmeConfigBlock.FindSubmatch(src)
	if m == nil {
		t.Fatal("no ~/.config/f9s/config.yaml block found in README.md")
	}

	dec := yaml.NewDecoder(bytes.NewReader(m[1]))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("README config block does not parse: %v", err)
	}

	if len(cfg.Contexts) == 0 {
		t.Fatal("README config block declares no contexts")
	}
	if _, ok := cfg.Contexts[cfg.CurrentContext]; !ok {
		t.Errorf("currentContext %q is not one of the contexts it defines", cfg.CurrentContext)
	}
	for name, cctx := range cfg.Contexts {
		if cctx.URL == "" {
			t.Errorf("context %q has no url", name)
		}
	}
}
