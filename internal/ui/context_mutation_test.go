package ui

import (
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/views"
)

func TestContextSwitchWaitsForRunningMutation(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := &config.Config{Contexts: map[string]config.Context{
		"a": {URL: "http://cluster-a.invalid"},
		"b": {URL: "http://cluster-b.invalid"},
	}}
	app := New(cfg, "a", 2, nil, nil)

	// Returning this command hands the mutation to Bubble Tea's async runtime.
	// Do not execute it: the test controls completion with cancelDoneMsg below.
	if cmd := app.startAction(views.ActionCancel, "job-a", "orders", ""); cmd == nil {
		t.Fatal("cancel action returned no command")
	}

	app.Update(views.SwitchContextMsg{Name: "b"})
	if app.contextName != "a" {
		t.Fatalf("switched to %q while a mutation was still running", app.contextName)
	}

	app.Update(cancelDoneMsg{name: "orders"})
	app.Update(views.SwitchContextMsg{Name: "b"})
	if app.contextName != "b" {
		t.Fatalf("context remained %q after the mutation completed", app.contextName)
	}
}
