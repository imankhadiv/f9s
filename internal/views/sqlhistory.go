package views

import (
	"bufio"
	"os"
	"path/filepath"
)

// sqlHistoryPath is $XDG_DATA_HOME/f9s/sql-history/<ctx>.txt.
func sqlHistoryPath(ctx string) string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join("sql-history", ctx+".txt")
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "f9s", "sql-history", ctx+".txt")
}

// loadSQLHistory reads past statements, oldest first, capped at 500.
func loadSQLHistory(ctx string) []string {
	f, err := os.Open(sqlHistoryPath(ctx))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			out = append(out, line)
		}
	}
	if len(out) > 500 {
		out = out[len(out)-500:]
	}
	return out
}

// appendSQLHistory persists one executed statement (single line).
func appendSQLHistory(ctx, stmt string) {
	path := sqlHistoryPath(ctx)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(stmt + "\n")
}
