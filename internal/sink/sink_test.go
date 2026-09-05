package sink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSinkBodiesAndFilter(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, string(b))
		mu.Unlock()
	}))
	defer srv.Close()

	open := Event{Type: "alert_open", Kind: "skew", Job: "orders", Vertex: "agg", Peak: "100%", Context: "prod", Time: time.Now()}
	clear := Event{Type: "alert_close", Kind: "backpressure", Job: "orders", Context: "prod", Time: time.Now()}

	t.Run("slack shape", func(t *testing.T) {
		s, _ := Build([]Config{{Type: "slack", URL: srv.URL}})
		mu.Lock()
		got = nil
		mu.Unlock()
		if errs := Dispatch(context.Background(), s, open); errs != nil {
			t.Fatal(errs)
		}
		var payload map[string]string
		json.Unmarshal([]byte(got[0]), &payload)
		if !strings.Contains(payload["text"], "skew FIRED") || !strings.Contains(payload["text"], "peak 100%") {
			t.Fatalf("bad slack text: %s", payload["text"])
		}
	})

	t.Run("openOnly filter drops clears", func(t *testing.T) {
		s, _ := Build([]Config{{Type: "webhook", URL: srv.URL, OpenOnly: true}})
		mu.Lock()
		got = nil
		mu.Unlock()
		Dispatch(context.Background(), s, clear)
		if len(got) != 0 {
			t.Fatalf("openOnly forwarded a clear: %v", got)
		}
		Dispatch(context.Background(), s, open)
		if len(got) != 1 {
			t.Fatalf("openOnly dropped an open: %d", len(got))
		}
	})

	t.Run("minKind filter", func(t *testing.T) {
		s, _ := Build([]Config{{Type: "webhook", URL: srv.URL, MinKind: "skew"}})
		mu.Lock()
		got = nil
		mu.Unlock()
		Dispatch(context.Background(), s, clear) // backpressure — filtered
		Dispatch(context.Background(), s, open)  // skew — passes
		if len(got) != 1 {
			t.Fatalf("minKind: want 1 delivery, got %d", len(got))
		}
	})

	t.Run("alertmanager array with endsAt on clear", func(t *testing.T) {
		s, _ := Build([]Config{{Type: "alertmanager", URL: srv.URL}})
		mu.Lock()
		got = nil
		mu.Unlock()
		Dispatch(context.Background(), s, clear)
		var arr []map[string]any
		json.Unmarshal([]byte(got[0]), &arr)
		if len(arr) != 1 || arr[0]["endsAt"] == nil {
			t.Fatalf("alertmanager clear missing endsAt: %s", got[0])
		}
	})

	t.Run("unknown type errors", func(t *testing.T) {
		if _, err := Build([]Config{{Type: "carrierpigeon"}}); err == nil {
			t.Fatal("unknown sink type must error, not silently drop")
		}
	})
}

func TestOneFailingSinkDoesNotBlockOthers(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	s, _ := Build([]Config{{Type: "webhook", URL: "http://127.0.0.1:1"}, {Type: "webhook", URL: ok.URL}})
	errs := Dispatch(context.Background(), s, Event{Type: "alert_open", Kind: "skew", Job: "j", Time: time.Now()})
	if len(errs) != 1 {
		t.Fatalf("want 1 error from the dead sink, got %d", len(errs))
	}
}
