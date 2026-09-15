// Package config loads f9s configuration: named cluster contexts in a
// kubeconfig-shaped YAML file, with a zero-config default of localhost:8081.
package config

import (
	"fmt"
	"github.com/imankhadiv/f9s/internal/sink"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Auth describes how to authenticate against a cluster's REST endpoint.
// Flink has no built-in auth; production clusters sit behind proxies, so
// this maps to headers on every request.
type Auth struct {
	Type     string `yaml:"type"`     // "", "bearer", "basic"
	Token    string `yaml:"token"`    // literal token (prefer tokenEnv)
	TokenEnv string `yaml:"tokenEnv"` // env var holding the token
	User     string `yaml:"user"`
	PassEnv  string `yaml:"passEnv"`
}

// AI configures the explain feature per context. Prod clusters can set
// policy: deny so no data ever leaves them; anonymization defaults to on.
type AI struct {
	Policy   string `yaml:"policy"`   // "", "allow", or "deny"
	Provider string `yaml:"provider"` // "ollama", "openai" (any compatible endpoint), or "anthropic"
	Model    string `yaml:"model"`
	URL      string `yaml:"url"`      // base URL for ollama/openai-compatible
	TokenEnv string `yaml:"tokenEnv"` // env var holding the API key
	// Anonymize replaces job names, paths, and hosts before any request;
	// nil means true.
	Anonymize *bool `yaml:"anonymize"`
	// Watch auto-runs a background investigation when a sentinel alert
	// opens (deduped, budgeted). Off unless explicitly enabled.
	Watch bool `yaml:"watch"`
	// Preview shows the exact (scrubbed) evidence text and waits for
	// confirmation before anything is sent to the model.
	Preview bool `yaml:"preview"`
}

// AnonymizeOn reports whether anonymization is enabled (default yes).
func (a AI) AnonymizeOn() bool { return a.Anonymize == nil || *a.Anonymize }

// Context is one named Flink cluster.
type Context struct {
	URL           string            `yaml:"url"`
	Auth          Auth              `yaml:"auth"`
	TLSSkipVerify bool              `yaml:"tlsSkipVerify"`
	Headers       map[string]string `yaml:"headers"`
	// KubeContext is the kubeconfig context used by the FlinkDeployment view.
	// Empty disables that view so kubectl never falls back to ambient state.
	KubeContext string `yaml:"kubeContext"`
	// SavepointDir prefills the target-directory prompt for savepoint and
	// stop-with-savepoint. Empty uses the cluster's state.savepoints.dir.
	SavepointDir string `yaml:"savepointDir"`
	// ReadOnly disables every mutating action on this context — savepoint,
	// stop, cancel, rescale, jar submit/delete, AI proposal execution.
	ReadOnly bool `yaml:"readOnly"`
	// Notify emits an OSC 9 desktop notification + terminal bell when an
	// alert opens, so a backgrounded f9s still reaches you.
	Notify bool `yaml:"notify"`
	// StateVolumeGB is the checkpoint/state volume size in GiB; when set,
	// the checkpoints view projects a disk-full ETA from the state-growth
	// trend.
	StateVolumeGB int `yaml:"stateVolumeGB"`
	// CostPerSlotHour is the money per task-slot-hour; when set, the waste
	// finder attributes idle-slot cost per job.
	CostPerSlotHour float64 `yaml:"costPerSlotHour"`
	// SQLGateway is the Flink SQL Gateway URL for this cluster (port 8083
	// by convention); empty disables the SQL console.
	SQLGateway string `yaml:"sqlGateway"`
	// HistoryServer is the Flink History Server URL (port 8082 by
	// convention); empty disables the :history view.
	HistoryServer string `yaml:"historyServer"`
	AI            AI     `yaml:"ai"`
	// Sinks forward opened/cleared alerts to outside systems
	// (Slack/webhook/pagerduty/alertmanager).
	Sinks []sink.Config `yaml:"sinks"`
}

// Config is the full f9s configuration file.
type Config struct {
	RefreshRate    int                `yaml:"refreshRate"` // seconds, default 2
	CurrentContext string             `yaml:"currentContext"`
	Contexts       map[string]Context `yaml:"contexts"`
	// Path is where this config was loaded from (or would be written).
	Path string `yaml:"-"`
}

// DefaultPath returns the XDG-style config location.
func DefaultPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "f9s", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".config", "f9s", "config.yaml")
}

// Load reads the config file at path. A missing file is not an error: it
// returns a default config with a single "local" context.
func Load(path string) (*Config, error) {
	cfg := &Config{
		Path:           path,
		RefreshRate:    2,
		CurrentContext: "local",
		Contexts:       map[string]Context{"local": {URL: "http://localhost:8081"}},
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.RefreshRate <= 0 {
		cfg.RefreshRate = 2
	}
	if len(cfg.Contexts) == 0 {
		cfg.Contexts = map[string]Context{"local": {URL: "http://localhost:8081"}}
	}
	if _, ok := cfg.Contexts[cfg.CurrentContext]; !ok {
		for name := range cfg.Contexts {
			cfg.CurrentContext = name
			break
		}
	}
	return cfg, nil
}

// Resolve returns the context by name, or the current one for "".
func (c *Config) Resolve(name string) (string, Context, error) {
	if name == "" {
		name = c.CurrentContext
	}
	ctx, ok := c.Contexts[name]
	if !ok {
		return "", Context{}, fmt.Errorf("context %q not found in config", name)
	}
	return name, ctx, nil
}

// BearerToken resolves the effective bearer token, if any.
func (a Auth) BearerToken() string {
	if a.TokenEnv != "" {
		return os.Getenv(a.TokenEnv)
	}
	return a.Token
}

// Save writes the config back to its path (0600 — it may name token env
// vars and internal URLs). Secrets themselves are never stored: AI tokens
// stay as environment-variable names.
func Save(cfg *Config) error {
	path := cfg.Path
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
