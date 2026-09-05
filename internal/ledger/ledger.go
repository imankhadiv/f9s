// Package ledger is f9s's savepoint memory. Flink forgets savepoint paths
// the moment the trigger response scrolls by; the ledger appends every
// completed savepoint to a per-context JSONL file so "restore from the one
// we took before Tuesday's deploy" is a lookup, not archaeology.
package ledger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Entry is one completed savepoint.
type Entry struct {
	Time   time.Time `json:"time"`
	JID    string    `json:"jid"`
	Job    string    `json:"job"`
	Action string    `json:"action"` // "savepoint" or "stop"
	Path   string    `json:"path"`
}

// Dir returns the XDG-style data directory for ledger files.
func Dir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "f9s", "ledger")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "ledger"
	}
	return filepath.Join(home, ".local", "share", "f9s", "ledger")
}

func file(contextName string) string {
	return filepath.Join(Dir(), contextName+".jsonl")
}

// Append records one savepoint for the given context.
func Append(contextName string, e Entry) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(file(contextName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// List returns the context's entries, newest first. A missing file returns
// an empty list.
func List(contextName string) ([]Entry, error) {
	f, err := os.Open(file(contextName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, sc.Err()
}
