package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/audit"
	"github.com/imankhadiv/f9s/internal/kube"
)

const crJSON = `{
  "metadata": {"namespace": "stream", "name": "orders", "generation": 2},
  "spec": {"job": {"parallelism": 2, "upgradeMode": "stateless"},
           "flinkConfiguration": {"pipeline.jobvertex-parallelism-overrides": "bc764cd8ddf7a0cff126f51c16239658:3"}},
  "status": {"observedGeneration": 2, "reconciliationStatus": {"state": "DEPLOYED"}}
}`

func fakeOperator(t *testing.T) *[]string {
	t.Helper()
	var patches []string
	t.Cleanup(kube.SetRunner(func(_ context.Context, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, " get ") {
			return []byte(crJSON), nil
		}
		if !strings.Contains(joined, "--dry-run") {
			patches = append(patches, args[len(args)-1]) // the -p body
		}
		return []byte("patched"), nil
	}))
	return &patches
}

func TestOperatorToolsListedOnlyWithKubeContext(t *testing.T) {
	has := func(reg *Registry) bool {
		for _, tl := range reg.List(true) {
			if tl.Name == "k8s_restart_job" {
				return true
			}
		}
		return false
	}
	if has(New(nil, "dev")) {
		t.Fatal("operator tools must not be offered without a kubeContext")
	}
	if !has(New(nil, "dev", WithKube("prod-us"))) {
		t.Fatal("operator tools missing with a kubeContext")
	}
	for _, tl := range New(nil, "dev", WithKube("k")).List(false) {
		if tl.NeedsKube {
			t.Fatalf("%s leaked into the read-only list", tl.Name)
		}
	}
	if _, err := New(nil, "dev").RunAudited(context.Background(), "k8s_restart_job",
		map[string]string{"namespace": "a", "name": "b"}, "test"); err == nil || !strings.Contains(err.Error(), "kubeContext") {
		t.Fatalf("run without kubeContext = %v", err)
	}
}

func TestParallelismOverrideMergesLiveCR(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	patches := fakeOperator(t)
	reg := New(nil, "dev", WithKube("prod-us"))
	out, err := reg.RunAudited(context.Background(), "set_parallelism_override", map[string]string{
		"namespace": "stream", "name": "orders", "vertex": "6d2677a0ecc3fd8df0b72ec675edf8f4", "parallelism": "5"}, "tui")
	if err != nil {
		t.Fatal(err)
	}
	want := `"pipeline.jobvertex-parallelism-overrides":"6d2677a0ecc3fd8df0b72ec675edf8f4:5,bc764cd8ddf7a0cff126f51c16239658:3"`
	if len(*patches) != 1 || !strings.Contains((*patches)[0], want) {
		t.Fatalf("patch = %v", *patches)
	}
	if !strings.Contains(out, "stream/orders") {
		t.Fatalf("out = %q", out)
	}
	recs, _ := audit.List("dev")
	if len(recs) == 0 || recs[len(recs)-1].Target != "stream/orders" || recs[len(recs)-1].Source != "tui" {
		t.Fatalf("audit = %+v", recs)
	}
}

func TestOperatorToolValidation(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	patches := fakeOperator(t)
	reg := New(nil, "dev", WithKube("prod-us"))
	bad := []struct {
		tool string
		args map[string]string
	}{
		{"set_target_utilization", map[string]string{"value": "1.5"}},
		{"set_autoscaler_enabled", map[string]string{"enabled": "yes"}},
		{"k8s_set_parallelism", map[string]string{"parallelism": "0"}},
		{"set_parallelism_override", map[string]string{"vertex": "Map", "parallelism": "2"}},
		{"k8s_restart_job", map[string]string{"kind": "pod"}},
	}
	for _, b := range bad {
		args := map[string]string{"namespace": "stream", "name": "orders"}
		for k, v := range b.args {
			args[k] = v
		}
		if _, err := reg.RunAudited(context.Background(), b.tool, args, "test"); err == nil {
			t.Errorf("%s %v should be rejected", b.tool, b.args)
		}
	}
	if len(*patches) != 0 {
		t.Fatalf("invalid calls must never patch: %v", *patches)
	}
	out, err := reg.RunAudited(context.Background(), "k8s_set_parallelism",
		map[string]string{"namespace": "stream", "name": "orders", "parallelism": "4"}, "test")
	if err != nil || !strings.Contains(out, "2 → 4") || !strings.Contains(out, "state is DROPPED") {
		t.Fatalf("stateless parallelism change must warn: %q %v", out, err)
	}
}
