package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/imankhadiv/f9s/internal/config"
)

// Command-mode navigation must keep the populated root jobs view: messages
// only reach the top of the stack, so a fresh root under the target would
// never receive its first fetch and esc would land on an empty list.
func TestCommandKeepsRootJobsView(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := &config.Config{Contexts: map[string]config.Context{"a": {URL: "http://cluster-a.invalid"}}}
	app := New(cfg, "a", 2, nil, nil)
	root := app.stack[0]

	for _, cmd := range []string{"jars", "timeline", "doctor", "jobs"} {
		app.runCommand(cmd)
		if app.stack[0] != root {
			t.Fatalf(":%s replaced the root jobs view", cmd)
		}
		app.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if len(app.stack) != 1 || app.stack[0] != root {
			t.Fatalf("esc after :%s did not return to the original jobs view", cmd)
		}
	}
}

// Before the first response the jobs view must not claim the cluster is empty.
func TestJobsViewSaysLoadingBeforeFirstFetch(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := &config.Config{Contexts: map[string]config.Context{"a": {URL: "http://cluster-a.invalid"}}}
	app := New(cfg, "a", 2, nil, nil)
	out := app.stack[0].View(120, 30)
	if strings.Contains(out, "No jobs on this cluster") {
		t.Fatal("jobs view claims an empty cluster before any fetch completed")
	}
	if !strings.Contains(out, "Loading jobs") {
		t.Fatalf("expected a loading state, got:\n%s", out)
	}
}
