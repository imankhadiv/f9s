package views

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/ledger"
)

func restoreTestJars(t *testing.T) (*Jars, *[]map[string]any) {
	t.Helper()
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/jars/job.jar/run" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		_, _ = w.Write([]byte(`{"jobid":"restored-job"}`))
	}))
	t.Cleanup(srv.Close)
	cfg := &config.Config{
		CurrentContext: "test",
		Contexts: map[string]config.Context{
			"test": {URL: srv.URL},
		},
	}
	return NewJars(Deps{CL: client.New(cfg.Contexts["test"]), Ctx: "test", Cfg: cfg}), &bodies
}

func applyRestoreCheck(t *testing.T, j *Jars, path string, risk restoreRisk) any {
	t.Helper()
	_, cmd := j.Update(restorePreflightMsg{
		owner: j, jarID: "job.jar", savepoint: path,
		check: restoreCheck{risk: risk, message: "preflight warning"},
	})
	if cmd == nil {
		return nil
	}
	return cmd()
}

func TestUnsafeRestoreOverrideIsScopedAndOneShot(t *testing.T) {
	j, bodies := restoreTestJars(t)
	first := applyRestoreCheck(t, j, "s3://state/one", restoreUnsafe)
	flash, ok := first.(FlashMsg)
	if !ok || !flash.IsErr || !strings.Contains(flash.Text, "same path") {
		t.Fatalf("first result = %#v", first)
	}
	if len(*bodies) != 0 {
		t.Fatal("first unsafe check submitted a restore")
	}

	second := applyRestoreCheck(t, j, "s3://state/one", restoreUnsafe)
	if action, ok := second.(jarActionMsg); !ok || action.err != nil {
		t.Fatalf("second result = %#v", second)
	}
	if len(*bodies) != 1 || (*bodies)[0]["allowNonRestoredState"] != true {
		t.Fatalf("unsafe bodies = %#v", *bodies)
	}

	third := applyRestoreCheck(t, j, "s3://state/one", restoreUnsafe)
	if _, ok := third.(FlashMsg); !ok || len(*bodies) != 1 {
		t.Fatalf("one-shot override was reused: msg=%#v bodies=%#v", third, *bodies)
	}
}

func TestUnknownRestoreConfirmationRemainsStrict(t *testing.T) {
	j, bodies := restoreTestJars(t)
	applyRestoreCheck(t, j, "s3://state/unknown", restoreUnknown)
	result := applyRestoreCheck(t, j, "s3://state/unknown", restoreUnknown)
	if action, ok := result.(jarActionMsg); !ok || action.err != nil {
		t.Fatalf("result = %#v", result)
	}
	if len(*bodies) != 1 || (*bodies)[0]["allowNonRestoredState"] != false {
		t.Fatalf("unknown preflight must restore strictly: %#v", *bodies)
	}
}

func TestRestoreApprovalDoesNotCrossSavepointPaths(t *testing.T) {
	j, bodies := restoreTestJars(t)
	applyRestoreCheck(t, j, "s3://state/one", restoreUnsafe)
	result := applyRestoreCheck(t, j, "s3://state/two", restoreUnsafe)
	if _, ok := result.(FlashMsg); !ok {
		t.Fatalf("changed path result = %#v", result)
	}
	if len(*bodies) != 0 {
		t.Fatalf("approval crossed paths: %#v", *bodies)
	}
}

func TestSafeRestoreRunsStrictlyWithoutConfirmation(t *testing.T) {
	j, bodies := restoreTestJars(t)
	result := applyRestoreCheck(t, j, "s3://state/safe", restoreSafe)
	if action, ok := result.(jarActionMsg); !ok || action.err != nil {
		t.Fatalf("result = %#v", result)
	}
	if len(*bodies) != 1 || (*bodies)[0]["allowNonRestoredState"] != false {
		t.Fatalf("safe restore body = %#v", *bodies)
	}
}

func preflightTestDeps(t *testing.T, jarResponse, originResponse string) Deps {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/jars/job.jar/plan":
			if jarResponse == "" {
				http.Error(w, "plan unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(jarResponse))
		case r.Method == http.MethodGet && r.URL.Path == "/jobs/origin/plan":
			if originResponse == "" {
				http.Error(w, "origin unavailable", http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(originResponse))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	cfg := &config.Config{
		CurrentContext: "test",
		Contexts: map[string]config.Context{
			"test": {URL: srv.URL},
		},
	}
	return Deps{CL: client.New(cfg.Contexts["test"]), Ctx: "test", Cfg: cfg}
}

func TestRestorePreflightClassifiesEvidence(t *testing.T) {
	t.Run("jar plan unavailable is unknown", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		deps := preflightTestDeps(t, "", "")
		if got := restorePreflight(deps, "job.jar", "s3://state/sp"); got.risk != restoreUnknown {
			t.Fatalf("check = %#v", got)
		}
	})

	t.Run("unknown savepoint origin is unknown", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		deps := preflightTestDeps(t, `{"plan":{"nodes":[{"id":"source"}]}}`, "")
		if got := restorePreflight(deps, "job.jar", "s3://state/sp"); got.risk != restoreUnknown {
			t.Fatalf("check = %#v", got)
		}
	})

	t.Run("missing operator is unsafe", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		deps := preflightTestDeps(t,
			`{"plan":{"nodes":[{"id":"source","description":"Source"}]}}`,
			`{"plan":{"nodes":[{"id":"source","description":"Source"},{"id":"stateful","description":"Keyed state"}]}}`,
		)
		if err := ledger.Append("test", ledger.Entry{
			Time: time.Now(), JID: "origin", Path: "s3://state/sp",
		}); err != nil {
			t.Fatal(err)
		}
		got := restorePreflight(deps, "job.jar", "s3://state/sp")
		if got.risk != restoreUnsafe || !strings.Contains(got.message, "Keyed state") {
			t.Fatalf("check = %#v", got)
		}
	})

	t.Run("matching operators are safe", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		plan := `{"plan":{"nodes":[{"id":"source","description":"Source"}]}}`
		deps := preflightTestDeps(t, plan, plan)
		if err := ledger.Append("test", ledger.Entry{
			Time: time.Now(), JID: "origin", Path: "s3://state/sp",
		}); err != nil {
			t.Fatal(err)
		}
		if got := restorePreflight(deps, "job.jar", "s3://state/sp"); got.risk != restoreSafe {
			t.Fatalf("check = %#v", got)
		}
	})
}

// A restore preflight decides whether to submit, so the verdict must only be
// applied by the view that asked for it. `:jars` rebuilds the view, and a
// context switch rebuilds it against a different cluster — a verdict landing
// on the replacement would submit the jar somewhere the operator never chose.
func TestRestorePreflightIgnoresVerdictFromReplacedView(t *testing.T) {
	oldView, oldBodies := restoreTestJars(t)
	currentView, currentBodies := restoreTestJars(t)

	_, cmd := currentView.Update(restorePreflightMsg{
		owner: oldView, jarID: "job.jar", savepoint: "s3://state/one",
		check: restoreCheck{risk: restoreSafe},
	})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("replaced view's verdict produced %#v", msg)
		}
	}
	if len(*currentBodies) != 0 {
		t.Fatalf("verdict from a replaced view submitted a restore: %#v", *currentBodies)
	}
	if len(*oldBodies) != 0 {
		t.Fatalf("replaced view submitted a restore: %#v", *oldBodies)
	}
	if currentView.approval != nil {
		t.Fatal("replaced view's verdict left an approval on the current view")
	}
}
