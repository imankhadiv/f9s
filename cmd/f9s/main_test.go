package main

import (
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

func testConfig(readOnly bool) *config.Config {
	return &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod": {
				URL:           "https://flink.prod.example.com",
				ReadOnly:      readOnly,
				Auth:          config.Auth{Type: "bearer", TokenEnv: "FLINK_PROD_TOKEN"},
				Headers:       map[string]string{"X-Tenant": "payments"},
				TLSSkipVerify: true,
				KubeContext:   "prod-us",
			},
		},
	}
}

// --read-only is documented as "disable every mutating action, regardless of
// context config", and a context's own readOnly: true is a standing policy.
// Neither may be lost because the operator also passed --address.
func TestReadOnlySurvivesAddressOverride(t *testing.T) {
	for _, tc := range []struct {
		name         string
		ctxReadOnly  bool
		flagReadOnly bool
		address      string
		wantReadOnly bool
	}{
		{name: "--read-only alone", flagReadOnly: true, wantReadOnly: true},
		{name: "--read-only with --address", flagReadOnly: true, address: "http://prod:8081", wantReadOnly: true},
		{name: "config policy alone", ctxReadOnly: true, wantReadOnly: true},
		{name: "config policy with --address", ctxReadOnly: true, address: "http://prod:8081", wantReadOnly: true},
		{name: "no policy stays writable", address: "http://prod:8081"},
		{name: "no policy, no address", wantReadOnly: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(tc.ctxReadOnly)
			name, cctx, err := resolveTarget(cfg, "", tc.address, tc.flagReadOnly)
			if err != nil {
				t.Fatal(err)
			}
			if cctx.ReadOnly != tc.wantReadOnly {
				t.Errorf("returned context ReadOnly = %v, want %v", cctx.ReadOnly, tc.wantReadOnly)
			}
			// readOnlyGuard and app.go read the policy back out of cfg by
			// name, so that lookup is what actually gates mutating actions.
			if got := cfg.Contexts[name].ReadOnly; got != tc.wantReadOnly {
				t.Errorf("cfg.Contexts[%q].ReadOnly = %v, want %v", name, got, tc.wantReadOnly)
			}
		})
	}
}

// The flip side: an address the operator typed may be a different cluster, so
// credentials meant for the configured host must not follow it there.
func TestAddressOverrideDoesNotCarryCredentials(t *testing.T) {
	cfg := testConfig(true)
	name, cctx, err := resolveTarget(cfg, "", "http://someone-elses-cluster:8081", false)
	if err != nil {
		t.Fatal(err)
	}
	if name == "prod" {
		t.Fatal("--address reused the configured context")
	}
	if cctx.Auth.Type != "" || cctx.Auth.TokenEnv != "" {
		t.Errorf("auth followed --address to another host: %#v", cctx.Auth)
	}
	if len(cctx.Headers) != 0 {
		t.Errorf("headers followed --address to another host: %#v", cctx.Headers)
	}
	if cctx.TLSSkipVerify {
		t.Error("tlsSkipVerify followed --address to another host")
	}
	if cctx.KubeContext != "" {
		t.Errorf("kubeContext followed --address to another host: %q", cctx.KubeContext)
	}
	if cctx.URL != "http://someone-elses-cluster:8081" {
		t.Errorf("URL = %q", cctx.URL)
	}
}
