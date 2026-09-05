package watchd

import (
	"bytes"
	"encoding/json"
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
	c.scrape = time.Now()

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
