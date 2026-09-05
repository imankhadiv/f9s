// Package audit appends every mutating action f9s performs — TUI keys, MCP
// tools, AI-approved proposals — to an exportable per-context JSONL trail:
// who, when, what, against which target, with what result.
package audit

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"time"
)

// Record is one mutating action.
type Record struct {
	Time    time.Time         `json:"time"`
	User    string            `json:"user"`
	Context string            `json:"context"`
	Action  string            `json:"action"`
	Target  string            `json:"target"`
	Params  map[string]string `json:"params,omitempty"`
	Result  string            `json:"result"` // "ok" or the error text
	Source  string            `json:"source"` // "tui" | "mcp" | "ai-proposal"
}

func dir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "audit"
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "f9s", "audit")
}

// Append writes one record (best-effort; auditing never blocks the action's
// result from reaching the user).
func Append(r Record) {
	if r.Time.IsZero() {
		r.Time = time.Now()
	}
	if r.User == "" {
		if u, err := user.Current(); err == nil {
			r.User = u.Username
		}
	}
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir(), r.Context+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	f.Write(append(data, '\n'))
}
