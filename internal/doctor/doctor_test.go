package doctor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
)

// fakeCluster serves crafted REST responses so Run's rule engine can be
// exercised end-to-end without a live Flink.
func fakeCluster(t *testing.T, routes map[string]string) *client.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := routes[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return client.New(config.Context{URL: srv.URL})
}

func findText(fs []Finding, sub string) *Finding {
	for i := range fs {
		if strings.Contains(fs[i].Text, sub) {
			return &fs[i]
		}
	}
	return nil
}

func TestRun_ClusterRules(t *testing.T) {
	// A deliberately unsafe cluster with no jobs: HA off, no savepoint dir,
	// web.cancel on, RocksDB non-incremental, debloat off.
	cl := fakeCluster(t, map[string]string{
		"/jobmanager/config": `[
			{"key":"high-availability.type","value":"NONE"},
			{"key":"web.cancel.enable","value":"true"},
			{"key":"state.backend.type","value":"rocksdb"},
			{"key":"state.backend.incremental","value":"false"}
		]`,
		"/jobs/overview": `{"jobs":[]}`,
	})
	fs, err := Run(cl)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"high-availability", "savepoint directory", "web.cancel", "RocksDB", "debloat"} {
		if findText(fs, want) == nil {
			t.Errorf("expected a finding mentioning %q; got %d findings", want, len(fs))
		}
	}
	if f := findText(fs, "RocksDB"); f != nil && f.Sev != SevWarn {
		t.Errorf("RocksDB finding should be a warning, got sev %v", f.Sev)
	}
}

func TestRun_HealthyClusterNoFindings(t *testing.T) {
	cl := fakeCluster(t, map[string]string{
		"/jobmanager/config": `[
			{"key":"high-availability.type","value":"kubernetes"},
			{"key":"state.savepoints.dir","value":"s3://sp"},
			{"key":"taskmanager.network.memory.buffer-debloat.enabled","value":"true"}
		]`,
		"/jobs/overview": `{"jobs":[]}`,
	})
	fs, err := Run(cl)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Sev != SevOK {
		t.Fatalf("healthy cluster should yield one OK finding, got %+v", fs)
	}
}

func TestRun_CheckpointingDisabledIsCritical(t *testing.T) {
	cl := fakeCluster(t, map[string]string{
		"/jobmanager/config": `[
			{"key":"high-availability.type","value":"kubernetes"},
			{"key":"state.savepoints.dir","value":"s3://sp"},
			{"key":"taskmanager.network.memory.buffer-debloat.enabled","value":"true"}
		]`,
		"/jobs/overview": `{"jobs":[{"jid":"abcdef1234567890","name":"stream","state":"RUNNING"}]}`,
		"/jobs/abcdef1234567890/checkpoints/config": `{"interval":0}`,
		"/jobs/abcdef1234567890/config":             `{"execution-config":{"restart-strategy":"fixed-delay"}}`,
	})
	fs, err := Run(cl)
	if err != nil {
		t.Fatal(err)
	}
	f := findText(fs, "checkpointing is DISABLED")
	if f == nil {
		t.Fatalf("expected a checkpointing-disabled finding, got %+v", fs)
	}
	if f.Sev != SevCritical {
		t.Errorf("disabled checkpointing should be CRITICAL, got %v", f.Sev)
	}
	if f.Area == "cluster" {
		t.Error("job finding should be scoped to the job name, not cluster")
	}
}

func TestRun_ClusterConfigErrorPropagates(t *testing.T) {
	cl := fakeCluster(t, map[string]string{}) // 404 on everything
	if _, err := Run(cl); err == nil {
		t.Fatal("expected an error when cluster config is unreachable")
	}
}
