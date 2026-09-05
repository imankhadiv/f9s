// Package pins persists the Metrics Explorer's pinned metrics per context.
package pins

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Pin identifies one pinned metric.
type Pin struct {
	Display string   `json:"display"`
	Path    string   `json:"path"`
	Keys    []string `json:"keys"`
	VID     string   `json:"vid,omitempty"`
	JID     string   `json:"jid,omitempty"`
	Par     int      `json:"par,omitempty"`
}

func file(contextName string) string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join("pins", contextName+".json")
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "f9s", "pins", contextName+".json")
}

// Load returns the context's pinned metrics; empty on any error.
func Load(contextName string) []Pin {
	data, err := os.ReadFile(file(contextName))
	if err != nil {
		return nil
	}
	var out []Pin
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return out
}

// Save writes the context's pinned metrics (best-effort).
func Save(contextName string, ps []Pin) {
	path := file(contextName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
