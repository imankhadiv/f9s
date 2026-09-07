package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/model"
)

// DeploymentMode reads execution.target off /jobmanager/config and classifies
// the cluster; an absent key stays Unknown (not Confident) so safety copy stays
// conservative.
func TestDeploymentModeFromConfig(t *testing.T) {
	cases := []struct {
		target    string
		want      model.DeploymentMode
		confident bool
	}{
		{"kubernetes-application", model.ModeApplication, true},
		{"yarn-application", model.ModeApplication, true},
		{"embedded", model.ModeApplication, true},
		{"kubernetes-session", model.ModeSession, true},
		{"remote", model.ModeSession, true},
		{"local", model.ModeStandalone, true},
		{"", model.ModeUnknown, false}, // key absent
	}
	for _, tc := range cases {
		body := `[]`
		if tc.target != "" {
			body = `[{"key":"execution.target","value":"` + tc.target + `"}]`
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		mi := New(config.Context{URL: srv.URL}).DeploymentMode()
		if mi.Mode != tc.want || mi.Confident != tc.confident {
			t.Errorf("target %q: got mode=%v confident=%v, want mode=%v confident=%v",
				tc.target, mi.Mode, mi.Confident, tc.want, tc.confident)
		}
		srv.Close()
	}
}

// A failed config read is not cached: the next call retries and can succeed.
func TestDeploymentModeRetriesAfterError(t *testing.T) {
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`[{"key":"execution.target","value":"kubernetes-session"}]`))
	}))
	defer srv.Close()
	c := New(config.Context{URL: srv.URL})
	if mi := c.DeploymentMode(); mi.Mode != model.ModeUnknown {
		t.Fatalf("error read should yield Unknown, got %v", mi.Mode)
	}
	fail = false
	if mi := c.DeploymentMode(); mi.Mode != model.ModeSession {
		t.Errorf("retry after error should detect session, got %v", mi.Mode)
	}
}
