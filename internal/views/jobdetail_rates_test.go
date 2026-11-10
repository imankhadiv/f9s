package views

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/model"
)

func TestJobDetailKeepsRecordRatesWhenBackpressureIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs/j1/vertices/v1/backpressure":
			http.Error(w, "backpressure sample not ready", http.StatusServiceUnavailable)
		case "/jobs/j1/vertices/v1/subtasks/metrics":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"id":"numRecordsInPerSecond","sum":1200},
				{"id":"numRecordsOutPerSecond","sum":1180}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	d := NewJobDetail(Deps{CL: client.New(config.Context{URL: srv.URL})}, "j1", "orders")
	d.job = model.Job{Vertices: []model.Vertex{{ID: "v1", Parallelism: 1}}}

	msg, ok := d.backpressureCmds()().(backpressureMsg)
	if !ok {
		t.Fatalf("sample returned an unexpected message type")
	}
	if msg.inRecs != 1200 || msg.outRecs != 1180 {
		t.Fatalf("record rates = %.0f/%.0f, want 1200/1180 despite backpressure failure", msg.inRecs, msg.outRecs)
	}
	if msg.pct != -1 {
		t.Fatalf("backpressure percent = %d, want unknown (-1)", msg.pct)
	}
}
