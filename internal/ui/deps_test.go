package ui

import (
	"reflect"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

// Every shared dependency must reach views through a.deps(); a field added
// to views.Deps but not wired here would silently be nil on some paths.
func TestAppDepsIsComplete(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := &config.Config{Contexts: map[string]config.Context{"dev": {URL: "http://localhost:1"}}}
	a := New(cfg, "dev", 2, nil, nil)
	v := reflect.ValueOf(a.deps())
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if name == "Plugins" { // legitimately empty without plugins.yaml
			continue
		}
		if v.Field(i).IsZero() {
			t.Errorf("a.deps() leaves %s unset", name)
		}
	}
}
