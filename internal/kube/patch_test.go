package kube

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const liveDeployment = `{
  "metadata": {"namespace": "stream", "name": "orders", "generation": 4},
  "spec": {"job": {"parallelism": 2, "upgradeMode": "savepoint", "savepointTriggerNonce": 5},
           "flinkConfiguration": {"pipeline.jobvertex-parallelism-overrides": "bc764cd8ddf7a0cff126f51c16239658:3"}},
  "status": {"lifecycleState": "STABLE", "observedGeneration": 4,
             "reconciliationStatus": {"state": "DEPLOYED"}}
}`

type kubectlCall []string

func fakeKubectl(t *testing.T, cr string, failDryRun bool) *[]kubectlCall {
	t.Helper()
	var calls []kubectlCall
	restore := SetRunner(func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, " get "):
			return []byte(cr), nil
		case strings.Contains(joined, "--dry-run=server") && failDryRun:
			return nil, errUnknownField
		}
		return []byte("flinkdeployment.flink.apache.org/orders patched"), nil
	})
	t.Cleanup(restore)
	return &calls
}

var errUnknownField = &kubectlErr{`error: strict decoding error: unknown field "spec.job.savepointRedeployNonce"`}

type kubectlErr struct{ s string }

func (e *kubectlErr) Error() string { return e.s }

func TestPatchDryRunsThenApplies(t *testing.T) {
	calls := fakeKubectl(t, liveDeployment, false)
	if err := Patch("prod", KindDeployment, "stream", "orders", ParallelismPatch(4)); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 3 {
		t.Fatalf("want get, dry run, apply — got %d calls: %v", len(*calls), *calls)
	}
	dry, apply := strings.Join((*calls)[1], " "), strings.Join((*calls)[2], " ")
	if !strings.Contains(dry, "--dry-run=server") || strings.Contains(apply, "--dry-run") {
		t.Fatalf("dry run / apply mixed up:\n%s\n%s", dry, apply)
	}
	for _, c := range *calls {
		if c[0] != "--context" || c[1] != "prod" {
			t.Fatalf("call not bound to the context: %v", c)
		}
	}
	if !strings.Contains(apply, `--type merge -p {"spec":{"job":{"parallelism":4}}}`) {
		t.Fatalf("apply = %s", apply)
	}
}

func TestPatchStopsWhenDryRunFails(t *testing.T) {
	calls := fakeKubectl(t, liveDeployment, true)
	err := Patch("prod", KindDeployment, "stream", "orders", RedeployPatch("s3://sp/1"))
	if err == nil || !strings.Contains(err.Error(), "dry run rejected") || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("err = %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("nothing may be applied after a failed dry run: %d calls", len(*calls))
	}
}

func TestPatchRefusesBusyOperator(t *testing.T) {
	for name, cr := range map[string]string{
		"upgrading": strings.Replace(liveDeployment, `"state": "DEPLOYED"`, `"state": "UPGRADING"`, 1),
		"drifting":  strings.Replace(liveDeployment, `"generation": 4`, `"generation": 5`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			calls := fakeKubectl(t, cr, false)
			err := Patch("prod", KindDeployment, "stream", "orders", RestartPatch())
			if err == nil || !strings.Contains(err.Error(), "refusing") {
				t.Fatalf("err = %v", err)
			}
			if len(*calls) != 1 {
				t.Fatalf("busy operator must stop before any patch: %v", *calls)
			}
		})
	}
}

func TestGetReadsLiveResource(t *testing.T) {
	fakeKubectl(t, liveDeployment, false)
	r, err := Get("prod", KindDeployment, "stream", "orders")
	if err != nil {
		t.Fatal(err)
	}
	if r.Busy() != "" || r.Job.Parallelism != 2 || r.FlinkConfig[OverridesKey] == "" {
		t.Fatalf("resource = %+v", r)
	}
	if _, err := Get("prod", "pods", "stream", "orders"); err == nil {
		t.Fatal("only operator kinds may be read/patched")
	}
}

func TestPatchBuilders(t *testing.T) {
	five := int64(5)
	sp := SavepointPatch(&JobSpec{SavepointTriggerNonce: &five})
	if n := sp["spec"].(map[string]any)["job"].(map[string]any)["savepointTriggerNonce"].(int64); n <= 5 {
		t.Fatalf("nonce must change, got %d", n)
	}
	v := "0.6"
	b, _ := json.Marshal(ConfigPatch(map[string]*string{"job.autoscaler.target.utilization": &v, OverridesKey: nil}))
	if !strings.Contains(string(b), `"job.autoscaler.target.utilization":"0.6"`) || !strings.Contains(string(b), `"`+OverridesKey+`":null`) {
		t.Fatalf("config patch = %s", b)
	}
	rd, _ := json.Marshal(RedeployPatch("s3://sp/9"))
	if !strings.Contains(string(rd), `"initialSavepointPath":"s3://sp/9"`) || !strings.Contains(string(rd), "savepointRedeployNonce") {
		t.Fatalf("redeploy patch = %s", rd)
	}
}

func TestMergeOverride(t *testing.T) {
	a, b := "bc764cd8ddf7a0cff126f51c16239658", "6d2677a0ecc3fd8df0b72ec675edf8f4"
	got, err := MergeOverride(a+":3", strings.ToUpper(b), 4)
	if err != nil || got != b+":4,"+a+":3" {
		t.Fatalf("merge = %q %v", got, err)
	}
	got, _ = MergeOverride(got, a, 0)
	if got != b+":4" {
		t.Fatalf("clear = %q", got)
	}
	if _, err := MergeOverride("", "Map", 2); err == nil {
		t.Fatal("a vertex name is not a vertex id")
	}
}
