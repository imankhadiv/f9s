package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

func TestJVMStatsDiscoversCollectorsOnce(t *testing.T) {
	var lists int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/taskmanagers/tm1/metrics" {
			t.Fatalf("path %s", r.URL.Path)
		}
		get := r.URL.Query().Get("get")
		if get == "" {
			atomic.AddInt32(&lists, 1)
			_, _ = w.Write([]byte(`[{"id":"Status.JVM.GarbageCollector.G1_Young_Generation.Time"},
				{"id":"Status.JVM.GarbageCollector.G1_Young_Generation.Count"},
				{"id":"Status.JVM.GarbageCollector.G1_Old_Generation.Time"},
				{"id":"Status.JVM.GarbageCollector.All.Time"},
				{"id":"Status.JVM.Memory.Heap.Used"}]`))
			return
		}
		if strings.Contains(get, "All.Time") {
			t.Errorf("aggregate All collector must not be summed with the per-collector ones: %s", get)
		}
		_, _ = w.Write([]byte(`[{"id":"Status.JVM.Memory.Heap.Used","value":"512"},
			{"id":"Status.JVM.Memory.Heap.Max","value":"1024"},
			{"id":"Status.JVM.GarbageCollector.G1_Young_Generation.Time","value":"300"},
			{"id":"Status.JVM.GarbageCollector.G1_Old_Generation.Time","value":"200"},
			{"id":"Status.JVM.GarbageCollector.G1_Young_Generation.Count","value":"9"}]`))
	}))
	defer srv.Close()
	cl := New(config.Context{URL: srv.URL})
	for i := 0; i < 3; i++ {
		st, err := cl.JVMStats("/taskmanagers/tm1/metrics")
		if err != nil {
			t.Fatal(err)
		}
		if st.HeapUsed != 512 || st.HeapMax != 1024 || st.GCTimeMs != 500 || st.GCCount != 9 {
			t.Fatalf("stats = %+v", st)
		}
		if st.MetaUsed != -1 || st.NetTotal != -1 {
			t.Fatalf("unreported metrics must stay -1: %+v", st)
		}
	}
	if lists != 1 {
		t.Fatalf("collector names listed %d times, want once", lists)
	}
}

func TestEmptyGCListingIsNotCached(t *testing.T) {
	var lists int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("get") == "" {
			atomic.AddInt32(&lists, 1)
			_, _ = w.Write([]byte(`[]`)) // young TM: metrics not registered yet
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	cl := New(config.Context{URL: srv.URL})
	_, _ = cl.JVMStats("/taskmanagers/t/metrics")
	_, _ = cl.JVMStats("/taskmanagers/t/metrics")
	if lists != 2 {
		t.Fatalf("an empty collector listing must be retried, listed %d times", lists)
	}
}
