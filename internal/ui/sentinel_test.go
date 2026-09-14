package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/sentinel"
)

func sentinelTestApp(t *testing.T, handler http.HandlerFunc) *App {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cfg := &config.Config{
		CurrentContext: "test",
		Contexts: map[string]config.Context{
			"test": {URL: srv.URL},
		},
	}
	return New(cfg, "test", 2, nil, nil)
}

func openCheckpointAlert(t *testing.T, a *App) {
	t.Helper()
	k := sentinel.Key{JID: "j1", VID: "job"}
	for range [3]int{} {
		a.sent.ObserveCondition(k, "checkpoint", "job", "checkpointing", true, time.Now())
	}
	if len(a.sent.OpenAlerts()) != 1 {
		t.Fatal("failed to arrange open checkpoint alert")
	}
}

func TestCollectSentinelMarksFailedInventoryNonAuthoritative(t *testing.T) {
	a := sentinelTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})
	msg := a.collectSentinel()().(sentinelMsg)
	if msg.authoritative {
		t.Fatal("failed jobs request must not be authoritative")
	}
}

func TestCollectSentinelKeepsListedJobLiveWhenDetailFails(t *testing.T) {
	a := sentinelTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jobs/overview" {
			_, _ = w.Write([]byte(`{"jobs":[{"jid":"j1","name":"job","state":"RUNNING"}]}`))
			return
		}
		http.Error(w, "detail unavailable", http.StatusServiceUnavailable)
	})
	msg := a.collectSentinel()().(sentinelMsg)
	if !msg.authoritative || !msg.liveJIDs["j1"] {
		t.Fatalf("message = %#v", msg)
	}
}

func TestSentinelUpdatePrunesOnlyAuthoritativeInventory(t *testing.T) {
	a := sentinelTestApp(t, func(http.ResponseWriter, *http.Request) {})
	openCheckpointAlert(t, a)
	a.Update(sentinelMsg{})
	if len(a.sent.OpenAlerts()) != 1 {
		t.Fatal("failed inventory pruned an alert")
	}

	a.Update(sentinelMsg{authoritative: true, liveJIDs: map[string]bool{"j1": true}})
	if len(a.sent.OpenAlerts()) != 1 {
		t.Fatal("listed job with unavailable detail was pruned")
	}

	a.Update(sentinelMsg{authoritative: true, liveJIDs: map[string]bool{}})
	if len(a.sent.OpenAlerts()) != 0 {
		t.Fatal("authoritative empty inventory did not prune alert")
	}
}

func TestSentinelUpdateDoesNotTreatUnknownCheckpointAsHealthy(t *testing.T) {
	a := sentinelTestApp(t, func(http.ResponseWriter, *http.Request) {})
	openCheckpointAlert(t, a)
	for range [5]int{} {
		a.Update(sentinelMsg{
			authoritative: true,
			liveJIDs:      map[string]bool{"j1": true},
			jobs: []jobHealth{{
				jid: "j1", job: "job", ckptKnown: false, numRestarts: -1,
			}},
		})
	}
	if len(a.sent.OpenAlerts()) != 1 {
		t.Fatal("unknown checkpoint telemetry cleared the alert")
	}
}

// ckptKnown is what lets a checkpoint observation count at all, so the
// collector must set it whenever /checkpoints answers. If it were ever left
// false the channel would go silent rather than noisy — alerts would stop
// firing *and* stop clearing, with nothing on screen to say so.
func TestCollectSentinelReportsCheckpointTelemetryAvailability(t *testing.T) {
	const overview = `{"jobs":[{"jid":"j1","name":"job","state":"RUNNING"}]}`
	const detail = `{"jid":"j1","name":"job","vertices":[]}`

	for _, tc := range []struct {
		name           string
		checkpoints    string
		wantKnown      bool
		wantCheckpoint bool
	}{
		{
			name:           "two failed checkpoints are known and bad",
			checkpoints:    `{"history":[{"status":"FAILED"},{"status":"FAILED"}]}`,
			wantKnown:      true,
			wantCheckpoint: true,
		},
		{
			name:        "completed checkpoints are known and healthy",
			checkpoints: `{"history":[{"status":"COMPLETED"},{"status":"COMPLETED"}]}`,
			wantKnown:   true,
		},
		{
			name:        "a job with no checkpoint history yet is still known",
			checkpoints: `{"history":[]}`,
			wantKnown:   true,
		},
		{
			name:        "an unreachable checkpoint endpoint is unknown",
			checkpoints: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := sentinelTestApp(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/jobs/overview":
					_, _ = w.Write([]byte(overview))
				case "/jobs/j1":
					_, _ = w.Write([]byte(detail))
				case "/jobs/j1/checkpoints":
					if tc.checkpoints == "" {
						http.Error(w, "checkpoints unavailable", http.StatusServiceUnavailable)
						return
					}
					_, _ = w.Write([]byte(tc.checkpoints))
				default:
					http.NotFound(w, r)
				}
			})
			msg := a.collectSentinel()().(sentinelMsg)
			if len(msg.jobs) != 1 {
				t.Fatalf("jobs = %#v", msg.jobs)
			}
			if got := msg.jobs[0].ckptKnown; got != tc.wantKnown {
				t.Errorf("ckptKnown = %v, want %v", got, tc.wantKnown)
			}
			if got := msg.jobs[0].ckptBad; got != tc.wantCheckpoint {
				t.Errorf("ckptBad = %v, want %v", got, tc.wantCheckpoint)
			}
		})
	}
}

// The whole point of the channel is that it still fires. Drive the real
// collector against a cluster whose checkpoints are failing and confirm an
// alert opens, then recovers.
func TestCheckpointAlertStillFiresAndClearsThroughCollector(t *testing.T) {
	checkpoints := `{"history":[{"status":"FAILED"},{"status":"FAILED"}]}`
	a := sentinelTestApp(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs/overview":
			_, _ = w.Write([]byte(`{"jobs":[{"jid":"j1","name":"job","state":"RUNNING"}]}`))
		case "/jobs/j1":
			_, _ = w.Write([]byte(`{"jid":"j1","name":"job","vertices":[]}`))
		case "/jobs/j1/checkpoints":
			_, _ = w.Write([]byte(checkpoints))
		default:
			http.NotFound(w, r)
		}
	})
	for range [4]int{} {
		a.Update(a.collectSentinel()())
	}
	if len(a.sent.OpenAlerts()) != 1 {
		t.Fatalf("checkpoint alert never fired through the collector: %#v", a.sent.OpenAlerts())
	}
	checkpoints = `{"history":[{"status":"COMPLETED"},{"status":"COMPLETED"}]}`
	for range [4]int{} {
		a.Update(a.collectSentinel()())
	}
	if len(a.sent.OpenAlerts()) != 0 {
		t.Fatalf("checkpoint alert never cleared: %#v", a.sent.OpenAlerts())
	}
}
