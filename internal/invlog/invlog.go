// Package invlog persists each AI investigation's audit trail to a session
// log file, so the trail survives the TUI session and can be attached to
// incident reports.
package invlog

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Logger appends one investigation's audit trail. All methods are nil-safe:
// a failed open degrades to no logging rather than breaking the diagnosis.
type Logger struct {
	f *os.File
}

// Dir is where investigation logs land: $XDG_DATA_HOME/f9s/investigations
// (default ~/.local/share/f9s/investigations).
func Dir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "investigations"
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "f9s", "investigations")
}

// New opens the log file for one investigation and writes its header.
func New(contextName, jid, job, trigger string) *Logger {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	short := jid
	if len(short) > 8 {
		short = short[:8]
	}
	name := fmt.Sprintf("%s-%s-%s.log", time.Now().Format("20060102-150405"), contextName, short)
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	l := &Logger{f: f}
	l.Line(fmt.Sprintf("investigation %s · context=%s jid=%s trigger=%s", job, contextName, jid, trigger))
	return l
}

// Line appends one timestamped line.
func (l *Logger) Line(s string) {
	if l == nil || l.f == nil {
		return
	}
	fmt.Fprintf(l.f, "%s  %s\n", time.Now().Format("15:04:05"), s)
}

// Close writes the outcome and closes the file.
func (l *Logger) Close(answer, errText string) {
	if l == nil || l.f == nil {
		return
	}
	if errText != "" {
		l.Line("ERROR: " + errText)
	}
	if answer != "" {
		fmt.Fprintf(l.f, "\nDIAGNOSIS\n%s\n", answer)
	}
	_ = l.f.Close()
	l.f = nil
}
