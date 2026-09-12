package views

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
)

func TestJobsIgnoresResultsFromReplacedView(t *testing.T) {
	oldServer := jobsOverviewServer(t, "old-job", "old-cluster-job")
	newServer := jobsOverviewServer(t, "new-job", "current-cluster-job")

	oldView := NewJobs(Deps{
		CL:  client.New(config.Context{URL: oldServer.URL}),
		Cfg: &config.Config{},
	})
	var staleCmd tea.Cmd
	for i := 0; i < 10; i++ {
		staleCmd = oldView.Refresh()
	}

	currentView := NewJobs(Deps{
		CL:  client.New(config.Context{URL: newServer.URL}),
		Cfg: &config.Config{},
	})
	freshCmd := currentView.Refresh()

	_, _ = currentView.Update(staleCmd())
	_, _ = currentView.Update(freshCmd())

	if len(currentView.all) != 1 || currentView.all[0].Name != "current-cluster-job" {
		t.Fatalf("current view applied stale jobs: %+v", currentView.all)
	}
}

func TestJobsIgnoresExtraResultsFromReplacedView(t *testing.T) {
	oldView := NewJobs(Deps{})
	currentView := NewJobs(Deps{})

	_, _ = currentView.Update(jobExtraMsg{
		owner: oldView,
		jid:   "shared-job",
		extra: jobExtra{restarts: "9"},
	})

	if _, ok := currentView.extras["shared-job"]; ok {
		t.Fatal("current view applied per-job data from a replaced view")
	}
}

func jobsOverviewServer(t *testing.T, jid, name string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/overview" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jobs":[{"jid":%q,"name":%q,"state":"FINISHED"}]}`, jid, name)
	}))
	t.Cleanup(srv.Close)
	return srv
}
