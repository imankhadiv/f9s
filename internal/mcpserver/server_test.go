package mcpserver

import (
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

func TestNewServerRejectsAllowWritesForReadOnlyContext(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod": {
				URL:      "http://localhost:8081",
				ReadOnly: true,
			},
		},
	}

	srv, err := newServer(cfg, "", "test", true)
	if err == nil {
		t.Fatal("expected read-only context with --allow-writes to be rejected")
	}
	if srv != nil {
		t.Fatal("expected no server when write policy is contradictory")
	}
	for _, want := range []string{`context "prod"`, "readOnly: true", "--allow-writes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestNewServerWriteCapability(t *testing.T) {
	for _, tt := range []struct {
		name        string
		readOnly    bool
		allowWrites bool
		wantWrites  bool
	}{
		{name: "default is read only"},
		{name: "flag enables writes", allowWrites: true, wantWrites: true},
		{name: "context policy without flag", readOnly: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				CurrentContext: "test",
				Contexts: map[string]config.Context{
					"test": {URL: "http://localhost:8081", ReadOnly: tt.readOnly},
				},
			}
			srv, err := newServer(cfg, "", "test", tt.allowWrites)
			if err != nil {
				t.Fatal(err)
			}
			got := srv.ListTools()
			for _, name := range []string{"trigger_savepoint", "stop_with_savepoint", "cancel_job"} {
				_, exists := got[name]
				if exists != tt.wantWrites {
					t.Errorf("tool %q present=%v, want %v", name, exists, tt.wantWrites)
				}
			}
			if _, exists := got["list_jobs"]; !exists {
				t.Error("read tool list_jobs must always be registered")
			}
		})
	}
}
