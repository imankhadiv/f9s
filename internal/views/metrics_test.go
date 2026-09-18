package views

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/model"
)

func TestMetricsRefreshIncludesSubtaskValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/jobs/j1/vertices/v1/subtasks/metrics":
			_, _ = w.Write([]byte(`[{"id":"numRecordsOut","value":"87"}]`))
		case "/jobs/j1/vertices/v1/metrics":
			_, _ = w.Write([]byte(`[{"id":"0.numRecordsOut","value":"174"},{"id":"1.numRecordsOut","value":"0"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	it := metricItem{
		display: "v / numRecordsOut",
		path:    "/jobs/j1/vertices/v1/subtasks/metrics",
		keys:    []string{"numRecordsOut"},
		vid:     "v1",
		jid:     "j1",
		par:     2,
	}
	m := &MetricsExplorer{
		deps:    Deps{CL: client.New(config.Context{URL: srv.URL})},
		inspect: &it,
	}

	msg := m.Refresh()().(metricValueMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if len(msg.vals) != 2 || msg.vals[0] != 174 || msg.vals[1] != 0 {
		t.Fatalf("subtask values = %v, want [174 0]", msg.vals)
	}
}

func TestMetricsRefreshReportsEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	it := metricItem{display: "empty", path: "/metrics", keys: []string{"missing"}}
	m := &MetricsExplorer{
		deps:    Deps{CL: client.New(config.Context{URL: srv.URL})},
		inspect: &it,
	}

	msg := m.Refresh()().(metricValueMsg)
	if msg.err == nil || msg.err.Error() != "fetch: no values returned" {
		t.Fatalf("error = %v, want %q", msg.err, "fetch: no values returned")
	}
}

func TestPinnedMetricsRefreshReportsEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	m := &MetricsExplorer{
		deps: Deps{CL: client.New(config.Context{URL: srv.URL})},
		pinned: []metricItem{{
			display: "empty",
			path:    "/metrics",
			keys:    []string{"missing"},
		}},
	}

	msg := m.Refresh()().(pinValueMsg)
	if msg.err == nil || msg.err.Error() != "pin: no values returned" {
		t.Fatalf("error = %v, want %q", msg.err, "pin: no values returned")
	}
}

func TestInspectorSubtaskBars(t *testing.T) {
	m := NewMetrics(Deps{Ctx: "test"}, model.JobOverview{JID: "j1", Name: "job"})
	it := metricItem{display: "v / numRecordsOut", path: "/x", keys: []string{"numRecordsOut"}, vid: "v1", jid: "j1", par: 2}
	m.inspect = &it
	mm, _ := m.Update(metricValueMsg{display: it.display, value: 87, agg: "min 0 · avg 87 · max 174", skew: 100, vals: []float64{174, 0}})
	m = mm.(*MetricsExplorer)
	out := m.View(120, 40)
	if !strings.Contains(out, "<s> shows the per-subtask bars") {
		t.Fatalf("hint missing:\n%s", out)
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = mm.(*MetricsExplorer)
	out = m.View(120, 40)
	if !strings.Contains(out, "#0") || !strings.Contains(out, "174.0") {
		t.Fatalf("bars missing after s:\n%s", out)
	}
}

func TestSearchPrefixJumpsScope(t *testing.T) {
	m := NewMetrics(Deps{Ctx: "test"}, model.JobOverview{})
	m.catalogs[scopeTM] = []metricItem{{display: "Status.JVM.Memory.Heap.Used", path: "/tm", keys: []string{"x"}}}
	m.input.SetValue("tm:heap")
	m.search()
	if m.scope != scopeTM {
		t.Fatalf("scope = %d, want TM", m.scope)
	}
	if len(m.matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(m.matches))
	}
}
