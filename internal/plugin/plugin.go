// Package plugin loads f9s's exec plugins: user-defined commands bound to
// keys, receiving the selected resource as JSON on stdin (k9s's plugins.yaml
// model). Plugins are user-authored local config — the same trust model as
// shell aliases.
package plugin

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Plugin is one configured command.
type Plugin struct {
	Name        string   `yaml:"-"`
	Key         string   `yaml:"key"`         // key that triggers it (jobs view)
	Description string   `yaml:"description"` // shown in the help bar
	Command     string   `yaml:"command"`
	Args        []string `yaml:"args"`
	Background  bool     `yaml:"background"` // true: run detached, flash the output
}

// Hook is a command run on lifecycle events (alert open/close), receiving
// the event as JSON on stdin.
type Hook struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

type file struct {
	Plugins map[string]Plugin `yaml:"plugins"`
	OnAlert *Hook             `yaml:"onAlert"`
}

// reserved keys that plugins may not shadow.
var reserved = map[string]bool{
	"q": true, "r": true, "?": true, "/": true, "enter": true, "esc": true,
	"c": true, "x": true, "s": true, "S": true, "ctrl+x": true, "p": true,
	"t": true, "C": true, "o": true, "a": true, "Q": true, "i": true, "K": true, "b": true, "!": true, "R": true, "d": true, "m": true, "tab": true, "A": true, "j": true, "k": true, "g": true, "G": true,
}

// DefaultPath returns the plugins file location next to the config.
func DefaultPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "f9s", "plugins.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "plugins.yaml"
	}
	return filepath.Join(home, ".config", "f9s", "plugins.yaml")
}

// Load reads the plugins file; a missing file returns no plugins. The
// second return is the onAlert hook, nil when unset.
func Load(path string) ([]Plugin, *Hook, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if f.OnAlert != nil && f.OnAlert.Command == "" {
		return nil, nil, fmt.Errorf("%s: onAlert needs a command", path)
	}
	out := make([]Plugin, 0, len(f.Plugins))
	for name, p := range f.Plugins {
		p.Name = name
		if p.Key == "" || p.Command == "" {
			return nil, nil, fmt.Errorf("plugin %q needs both key and command", name)
		}
		if reserved[p.Key] {
			return nil, nil, fmt.Errorf("plugin %q uses reserved key %q", name, p.Key)
		}
		out = append(out, p)
	}
	return out, f.OnAlert, nil
}
