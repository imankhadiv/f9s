package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CurrentContext != "local" || cfg.RefreshRate != 2 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if _, ok := cfg.Contexts["local"]; !ok {
		t.Error("default local context missing")
	}
}

func TestLoadResolveSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `currentContext: prod
refreshRate: 5
contexts:
  prod:
    url: https://flink.prod:8081
    readOnly: true
    stateVolumeGB: 500
    auth:
      type: bearer
      tokenEnv: FLINK_TOKEN
  staging:
    url: http://flink.staging:8081
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RefreshRate != 5 || cfg.CurrentContext != "prod" {
		t.Fatalf("scalars not parsed: %+v", cfg)
	}

	// Resolve: current, named, missing.
	name, ctx, err := cfg.Resolve("")
	if err != nil || name != "prod" || !ctx.ReadOnly || ctx.StateVolumeGB != 500 {
		t.Fatalf("Resolve(current) wrong: %q %+v %v", name, ctx, err)
	}
	if _, c, err := cfg.Resolve("staging"); err != nil || c.URL != "http://flink.staging:8081" {
		t.Fatalf("Resolve(staging) wrong: %+v %v", c, err)
	}
	if _, _, err := cfg.Resolve("ghost"); err == nil {
		t.Error("Resolve of unknown context should error")
	}

	// Auth resolution: env var beats literal.
	t.Setenv("FLINK_TOKEN", "s3cr3t")
	if tok := ctx.Auth.BearerToken(); tok != "s3cr3t" {
		t.Errorf("BearerToken from env = %q", tok)
	}

	// Save round-trip preserves the parsed config.
	cfg.Path = filepath.Join(t.TempDir(), "out.yaml")
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("saved config should be 0600, got %v", fi.Mode().Perm())
	}
	reloaded, err := Load(cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CurrentContext != "prod" || !reloaded.Contexts["prod"].ReadOnly {
		t.Fatalf("round-trip lost data: %+v", reloaded)
	}
}
