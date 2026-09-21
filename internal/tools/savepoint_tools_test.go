package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
)

func TestEveryMutatingToolCarriesRiskCopy(t *testing.T) {
	for _, tl := range New(nil, "x").List(true) {
		if tl.Mutating && strings.TrimSpace(tl.Risk) == "" {
			t.Errorf("%s is mutating but has no Risk copy for the confirm dialog", tl.Name)
		}
		if tl.Mutating && RiskOf(tl.Name) != tl.Risk {
			t.Errorf("RiskOf(%s) disagrees with the tool", tl.Name)
		}
	}
}

func TestTargetOf(t *testing.T) {
	cases := []struct {
		args map[string]string
		want string
	}{
		{map[string]string{"jid": "j1", "name": "x"}, "j1"},
		{map[string]string{"namespace": "stream", "name": "orders"}, "stream/orders"},
		{map[string]string{"savepoint_path": "s3://sp/1"}, "s3://sp/1"},
		{map[string]string{}, ""},
	}
	for _, c := range cases {
		if got := TargetOf(c.args); got != c.want {
			t.Errorf("TargetOf(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

// fakeFlink serves just enough REST for the savepoint tools.
func fakeFlink(t *testing.T, retain bool, onDispose func(), onRun func(body map[string]any)) *client.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/jobs/overview":
			_, _ = w.Write([]byte(`{"jobs":[{"jid":"j1","name":"orders","state":"RUNNING"}]}`))
		case r.URL.Path == "/jobs/j1/checkpoints":
			_, _ = w.Write([]byte(`{"history":[
			  {"id":9,"status":"COMPLETED","is_savepoint":false,"external_path":"s3://ck/9"},
			  {"id":8,"status":"COMPLETED","is_savepoint":true,"external_path":"s3://sp/newest"},
			  {"id":5,"status":"COMPLETED","is_savepoint":true,"external_path":"s3://sp/older"}]}`))
		case r.URL.Path == "/jobs/j1/checkpoints/config":
			if retain {
				_, _ = w.Write([]byte(`{"externalization":{"enabled":true,"delete_on_cancellation":false}}`))
			} else {
				_, _ = w.Write([]byte(`{"externalization":{"enabled":false,"delete_on_cancellation":true}}`))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/savepoint-disposal":
			onDispose()
			_, _ = w.Write([]byte(`{"request-id":"d1"}`))
		case r.URL.Path == "/savepoint-disposal/d1":
			_, _ = w.Write([]byte(`{"status":{"id":"COMPLETED"},"operation":{}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/jars/app.jar/run":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			onRun(body)
			_, _ = w.Write([]byte(`{"jobid":"new-job"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return client.New(config.Context{URL: srv.URL})
}

func TestDisposeRefusesTheOnlyRecoveryPoint(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	disposed := false
	reg := New(fakeFlink(t, false, func() { disposed = true }, nil), "dev")
	_, err := reg.RunAudited(context.Background(), "dispose_savepoint", map[string]string{"savepoint_path": "s3://sp/newest"}, "test")
	if err == nil || !strings.Contains(err.Error(), "only recovery point") || disposed {
		t.Fatalf("dispose of the newest savepoint of a non-retaining job: err=%v disposed=%v", err, disposed)
	}
}

func TestDisposeAllowsOlderOrRetainedSavepoints(t *testing.T) {
	if testing.Short() {
		t.Skip("polls with a 2s sleep")
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	disposed := 0
	reg := New(fakeFlink(t, true, func() { disposed++ }, nil), "dev")
	if _, err := reg.RunAudited(context.Background(), "dispose_savepoint", map[string]string{"savepoint_path": "s3://sp/newest"}, "test"); err != nil {
		t.Fatalf("retained checkpoints make the newest savepoint disposable: %v", err)
	}
	if disposed != 1 {
		t.Fatalf("disposal POSTs = %d", disposed)
	}
}

func TestRestoreFromSavepointSubmitsJar(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var got map[string]any
	reg := New(fakeFlink(t, true, nil, func(b map[string]any) { got = b }), "dev")
	out, err := reg.RunAudited(context.Background(), "restore_from_savepoint",
		map[string]string{"jar_id": "app.jar", "savepoint_path": "s3://sp/older"}, "test")
	if err != nil || !strings.Contains(out, "new-job") {
		t.Fatalf("restore = %q %v", out, err)
	}
	if got["savepointPath"] != "s3://sp/older" || got["allowNonRestoredState"] != false {
		t.Fatalf("run body = %v (allow_non_restored must default to false)", got)
	}
	if _, err := reg.RunAudited(context.Background(), "restore_from_savepoint", map[string]string{"jar_id": "app.jar"}, "test"); err == nil {
		t.Fatal("restore without a savepoint path must be refused")
	}
}
