package watchd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
)

func TestPrometheusExpositionValid(t *testing.T) {
	c := New(client.New(config.Context{URL: "http://127.0.0.1:1"}), "test", time.Second, nil)
	// seed gauges directly (no live cluster needed)
	c.vgauge = map[string]vertexGauge{
		"j|v1": {job: "orders", vertex: "src", bpLevel: 2, busy: 950, skew: 73},
		"j|v2": {job: "orders", vertex: "agg", bpLevel: 0, busy: 40, skew: -1},
	}
	c.jgauge = map[string]jobGauge{"j": {name: "orders", restarts: 3, bottleneck: true, openAlerts: 2}}
	c.up = true
	c.lastSuccess = time.Now()

	var b bytes.Buffer
	c.WriteMetrics(&b)
	out := b.String()

	// each metric name must have EXACTLY one HELP and one TYPE line — the
	// classic exposition-format bug is a duplicated header.
	for _, name := range []string{"f9s_up", "f9s_vertex_skew_percent", "f9s_job_restarts", "f9s_vertex_backpressure_level"} {
		if n := strings.Count(out, "# HELP "+name+" "); n != 1 {
			t.Errorf("%s: %d HELP lines, want 1\n%s", name, n, out)
		}
		if n := strings.Count(out, "# TYPE "+name+" "); n != 1 {
			t.Errorf("%s: %d TYPE lines, want 1", name, n)
		}
	}
	for _, want := range []string{
		`f9s_vertex_skew_percent{context="test",job="orders",vertex="src"} 73`,
		`f9s_vertex_backpressure_level{context="test",job="orders",vertex="src"} 2`,
		`f9s_job_restarts{context="test",job="orders"} 3`,
		`f9s_job_has_bottleneck{context="test",job="orders"} 1`,
		`f9s_job_open_alerts{context="test",job="orders"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing series: %s\n%s", want, out)
		}
	}
	// unknown skew (-1) must be omitted, not emitted as -1
	if strings.Contains(out, `vertex="agg"} -1`) {
		t.Error("unknown skew leaked a -1 series")
	}
}

func testCollector(t *testing.T, handler http.HandlerFunc, emit func(Event)) *Collector {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(client.New(config.Context{URL: srv.URL}), "test", time.Second, emit)
}

func renderedMetrics(c *Collector) string {
	var b bytes.Buffer
	c.WriteMetrics(&b)
	return b.String()
}

func liveJobHandler(failInventory, failDetail *bool, checkpointMode *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs/overview":
			if *failInventory {
				http.Error(w, "inventory unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"jobs":[{"jid":"j1","name":"orders","state":"RUNNING"}]}`))
		case "/jobs/j1":
			if *failDetail {
				http.Error(w, "detail unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"jid":"j1","name":"orders","state":"RUNNING","vertices":[]}`))
		case "/jobs/j1/checkpoints":
			switch *checkpointMode {
			case "failed":
				_, _ = w.Write([]byte(`{"history":[{"status":"FAILED"},{"status":"FAILED"}]}`))
			case "healthy":
				_, _ = w.Write([]byte(`{"history":[{"status":"COMPLETED"},{"status":"COMPLETED"}]}`))
			default:
				http.Error(w, "checkpoint telemetry unavailable", http.StatusServiceUnavailable)
			}
		case "/jobs/j1/metrics":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}
}

func TestScrapeHealthTracksLatestAuthoritativePoll(t *testing.T) {
	failInventory, failDetail := true, false
	checkpointMode := "healthy"
	c := testCollector(t, liveJobHandler(&failInventory, &failDetail, &checkpointMode), nil)

	if out := renderedMetrics(c); !strings.Contains(out, `f9s_up{context="test"} 0`) ||
		strings.Contains(out, `f9s_scrape_age_seconds{context="test"}`) {
		t.Fatalf("before first success:\n%s", out)
	}

	failInventory = false
	c.poll()
	if out := renderedMetrics(c); !strings.Contains(out, `f9s_up{context="test"} 1`) ||
		!strings.Contains(out, `f9s_scrape_age_seconds{context="test"}`) {
		t.Fatalf("after success:\n%s", out)
	}
	before := c.jgauge

	failInventory = true
	c.poll()
	if out := renderedMetrics(c); !strings.Contains(out, `f9s_up{context="test"} 0`) ||
		!strings.Contains(out, `f9s_scrape_age_seconds{context="test"}`) {
		t.Fatalf("after failure:\n%s", out)
	}
	if !reflect.DeepEqual(c.jgauge, before) {
		t.Fatalf("failure replaced last-good gauges: before=%#v after=%#v", before, c.jgauge)
	}
}

func TestIncompleteJobDetailPreservesLastGoodState(t *testing.T) {
	failInventory, failDetail := false, false
	checkpointMode := "healthy"
	c := testCollector(t, liveJobHandler(&failInventory, &failDetail, &checkpointMode), nil)
	c.poll()
	before := c.jgauge

	failDetail = true
	c.poll()
	if c.up {
		t.Fatal("incomplete detail sweep reported up")
	}
	if !reflect.DeepEqual(c.jgauge, before) {
		t.Fatalf("incomplete sweep replaced gauges: before=%#v after=%#v", before, c.jgauge)
	}
}

func TestUnknownCheckpointTelemetryDoesNotClearAlert(t *testing.T) {
	failInventory, failDetail := false, false
	checkpointMode := "failed"
	var events []Event
	c := testCollector(t, liveJobHandler(&failInventory, &failDetail, &checkpointMode), func(e Event) {
		events = append(events, e)
	})
	for range [3]int{} {
		c.poll()
	}
	if len(c.sent.OpenAlerts()) != 1 {
		t.Fatal("failed checkpoints did not open an alert")
	}

	checkpointMode = "unknown"
	for range [5]int{} {
		c.poll()
	}
	if len(c.sent.OpenAlerts()) != 1 {
		t.Fatal("unknown checkpoint telemetry cleared the alert")
	}
	for _, e := range events {
		if e.Type == "alert_close" && e.Kind == "checkpoint" {
			t.Fatal("unknown checkpoint telemetry emitted a close event")
		}
	}
}

func TestUnknownBackpressureTelemetryDoesNotClearAlert(t *testing.T) {
	bpMode := "high"
	var events []Event
	c := testCollector(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs/overview":
			_, _ = w.Write([]byte(`{"jobs":[{"jid":"j1","name":"orders","state":"RUNNING"}]}`))
		case "/jobs/j1":
			_, _ = w.Write([]byte(`{"jid":"j1","name":"orders","state":"RUNNING","vertices":[{"id":"v1","name":"source","parallelism":1}]}`))
		case "/jobs/j1/checkpoints":
			_, _ = w.Write([]byte(`{"history":[]}`))
		case "/jobs/j1/metrics":
			_, _ = w.Write([]byte(`[]`))
		case "/jobs/j1/vertices/v1/backpressure":
			if bpMode == "high" {
				_, _ = w.Write([]byte(`{"backpressureLevel":"high"}`))
				return
			}
			http.Error(w, "backpressure unavailable", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}, func(e Event) {
		events = append(events, e)
	})

	for range [3]int{} {
		c.poll()
	}
	if len(c.sent.OpenAlerts()) != 1 {
		t.Fatal("high backpressure did not open an alert")
	}

	bpMode = "unknown"
	for range [5]int{} {
		c.poll()
	}
	if len(c.sent.OpenAlerts()) != 1 {
		t.Fatal("unknown backpressure telemetry cleared the alert")
	}
	for _, e := range events {
		if e.Type == "alert_close" && e.Kind == "backpressure" {
			t.Fatal("unknown backpressure telemetry emitted a close event")
		}
	}
}

func TestJobFailedEventsAreTransitionsNotStartupHistory(t *testing.T) {
	state := "RUNNING"
	var events []Event
	c := testCollector(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs/overview":
			_, _ = w.Write([]byte(`{"jobs":[{"jid":"j1","name":"orders","state":"` + state + `"}]}`))
		case "/jobs/j1":
			_, _ = w.Write([]byte(`{"jid":"j1","name":"orders","state":"RUNNING","vertices":[]}`))
		case "/jobs/j1/checkpoints":
			_, _ = w.Write([]byte(`{"history":[]}`))
		case "/jobs/j1/metrics":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}, func(e Event) {
		events = append(events, e)
	})

	c.poll()
	state = "FAILED"
	c.poll()
	c.poll()
	state = "RUNNING"
	c.poll()

	var opened, closed int
	for _, e := range events {
		if e.Kind != "job-failed" {
			continue
		}
		if e.Type == "alert_open" {
			opened++
		}
		if e.Type == "alert_close" {
			closed++
		}
	}
	if opened != 1 || closed != 1 {
		t.Fatalf("job-failed transitions: open=%d close=%d events=%#v", opened, closed, events)
	}

	events = nil
	startedFailed := testCollector(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jobs/overview" {
			_, _ = w.Write([]byte(`{"jobs":[{"jid":"old","name":"old failure","state":"FAILED"}]}`))
			return
		}
		http.NotFound(w, r)
	}, func(e Event) {
		events = append(events, e)
	})
	startedFailed.poll()
	for _, e := range events {
		if e.Kind == "job-failed" {
			t.Fatalf("startup flooded historical failure: %#v", e)
		}
	}
}

func TestNDJSONSchema(t *testing.T) {
	var b bytes.Buffer
	emit := EncodeNDJSON(&b)
	emit(Event{Type: "alert_open", Kind: "skew", Job: "j", Context: "c", Schema: schemaVersion, Time: time.Now()})
	var e Event
	if err := json.Unmarshal(b.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if e.SchemaVersion() != 1 || e.Type != "alert_open" {
		t.Fatalf("bad event: %+v", e)
	}
}

// A job whose detail cannot be read makes the sweep incomplete, but the rest
// of the cluster must still be polled. Aborting the sweep would mean one
// unreadable job silences every other job's alerts for as long as it stays
// broken — the failure mode a headless collector can least afford.
func TestUnreadableJobDoesNotBlindTheRestOfTheCluster(t *testing.T) {
	c := testCollector(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs/overview":
			_, _ = w.Write([]byte(`{"jobs":[
				{"jid":"j1","name":"broken","state":"RUNNING"},
				{"jid":"j2","name":"healthy","state":"RUNNING"}]}`))
		case "/jobs/j1":
			http.Error(w, "detail unavailable", http.StatusServiceUnavailable)
		case "/jobs/j2":
			_, _ = w.Write([]byte(`{"jid":"j2","name":"healthy","state":"RUNNING","vertices":[]}`))
		case "/jobs/j2/checkpoints":
			_, _ = w.Write([]byte(`{"history":[{"status":"FAILED"},{"status":"FAILED"}]}`))
		case "/jobs/j2/metrics":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}, nil)
	for range [3]int{} {
		c.poll()
	}

	if c.up {
		t.Error("a sweep missing a job's detail must not report up")
	}
	if _, ok := c.jgauge["j2"]; !ok {
		t.Fatalf("readable job missing from gauges: %#v", c.jgauge)
	}
	alerts := c.sent.OpenAlerts()
	if len(alerts) != 1 || alerts[0].Kind != "checkpoint" || alerts[0].Job != "healthy" {
		t.Fatalf("readable job's checkpoint alert never fired: %#v", alerts)
	}
}

// The unreadable job keeps its own last-good gauges and its restart baseline,
// so its series neither disappear from the scrape nor reset the restart-loop
// detector when it comes back.
func TestUnreadableJobKeepsLastGoodGauges(t *testing.T) {
	failDetail := false
	c := testCollector(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs/overview":
			_, _ = w.Write([]byte(`{"jobs":[{"jid":"j1","name":"orders","state":"RUNNING"}]}`))
		case "/jobs/j1":
			if failDetail {
				http.Error(w, "detail unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"jid":"j1","name":"orders","state":"RUNNING","vertices":[{"id":"v1","name":"src","parallelism":1}]}`))
		case "/jobs/j1/checkpoints":
			_, _ = w.Write([]byte(`{"history":[{"status":"COMPLETED"},{"status":"COMPLETED"}]}`))
		case "/jobs/j1/metrics":
			_, _ = w.Write([]byte(`[{"id":"numRestarts","value":"4"}]`))
		case "/jobs/j1/vertices/v1/backpressure":
			_, _ = w.Write([]byte(`{"backpressureLevel":"ok"}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}, nil)
	c.poll()
	goodJobs, goodVerts, goodRestarts := c.jgauge, c.vgauge, c.lastRun["j1"]
	if goodRestarts != 4 {
		t.Fatalf("restart baseline = %d, want 4", goodRestarts)
	}

	failDetail = true
	c.poll()

	if !reflect.DeepEqual(c.jgauge, goodJobs) {
		t.Errorf("job gauges changed: before=%#v after=%#v", goodJobs, c.jgauge)
	}
	if !reflect.DeepEqual(c.vgauge, goodVerts) {
		t.Errorf("vertex gauges changed: before=%#v after=%#v", goodVerts, c.vgauge)
	}
	if got := c.lastRun["j1"]; got != goodRestarts {
		t.Errorf("restart baseline reset to %d, want %d", got, goodRestarts)
	}
}

// numRestarts is the only evidence the restart-loop detector has. When the
// metric cannot be read, "restarts did not increase" is indistinguishable
// from "restarts are unknown" — and the first would advance the recovery
// streak and close a firing alert during the outage.
func TestUnknownRestartTelemetryDoesNotClearAlert(t *testing.T) {
	restarts, failMetrics := 0, false
	c := testCollector(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/jobs/overview":
			_, _ = w.Write([]byte(`{"jobs":[{"jid":"j1","name":"orders","state":"RUNNING"}]}`))
		case r.URL.Path == "/jobs/j1":
			_, _ = w.Write([]byte(`{"jid":"j1","name":"orders","state":"RUNNING","vertices":[]}`))
		case r.URL.Path == "/jobs/j1/checkpoints":
			_, _ = w.Write([]byte(`{"history":[{"status":"COMPLETED"},{"status":"COMPLETED"}]}`))
		case r.URL.Path == "/jobs/j1/metrics":
			if failMetrics {
				http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
				return
			}
			restarts++
			fmt.Fprintf(w, `[{"id":"numRestarts","value":"%d"}]`, restarts)
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}, nil)
	for range [5]int{} {
		c.poll()
	}
	if !hasOpenKind(c, "restart-loop") {
		t.Fatalf("restart-loop alert never fired: %#v", c.sent.OpenAlerts())
	}

	failMetrics = true
	for range [5]int{} {
		c.poll()
	}
	if !hasOpenKind(c, "restart-loop") {
		t.Fatal("unknown restart telemetry cleared the alert")
	}
}

// Same shape for skew: an unreadable subtask-metrics call scores -1, which is
// below the clear threshold and would count as a clean recovery.
func TestUnknownSkewTelemetryDoesNotClearAlert(t *testing.T) {
	failSubtasks := false
	c := testCollector(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/jobs/overview":
			_, _ = w.Write([]byte(`{"jobs":[{"jid":"j1","name":"orders","state":"RUNNING"}]}`))
		case r.URL.Path == "/jobs/j1":
			_, _ = w.Write([]byte(`{"jid":"j1","name":"orders","state":"RUNNING","vertices":[{"id":"v1","name":"src","parallelism":2}]}`))
		case r.URL.Path == "/jobs/j1/checkpoints":
			_, _ = w.Write([]byte(`{"history":[{"status":"COMPLETED"},{"status":"COMPLETED"}]}`))
		case strings.HasPrefix(r.URL.Path, "/jobs/j1/vertices/v1/metrics"):
			if failSubtasks {
				http.Error(w, "subtask metrics unavailable", http.StatusServiceUnavailable)
				return
			}
			// one subtask takes everything: a lopsided vertex
			_, _ = w.Write([]byte(`[{"id":"0.numRecordsInPerSecond","value":"1000"},
				{"id":"1.numRecordsInPerSecond","value":"0"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}, nil)
	for range [5]int{} {
		c.poll()
	}
	if !hasOpenKind(c, "skew") {
		t.Fatalf("skew alert never fired: %#v", c.sent.OpenAlerts())
	}

	failSubtasks = true
	for range [5]int{} {
		c.poll()
	}
	if !hasOpenKind(c, "skew") {
		t.Fatal("unknown skew telemetry cleared the alert")
	}
}

func hasOpenKind(c *Collector, kind string) bool {
	for _, a := range c.sent.OpenAlerts() {
		if a.Kind == kind {
			return true
		}
	}
	return false
}
