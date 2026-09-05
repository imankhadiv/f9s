package client

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/config"
)

// The global request semaphore must cap concurrent REST calls at 20 no
// matter how wide the caller fans out.
func TestRequestConcurrencyCapped(t *testing.T) {
	var inFlight, peak int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt64(&inFlight, 1)
		for {
			p := atomic.LoadInt64(&peak)
			if cur <= p || atomic.CompareAndSwapInt64(&peak, p, cur) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt64(&inFlight, -1)
		w.Write([]byte(`{"jobs":[]}`))
	}))
	defer srv.Close()

	c := New(config.Context{URL: srv.URL})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Jobs()
		}()
	}
	wg.Wait()
	if p := atomic.LoadInt64(&peak); p > 20 {
		t.Fatalf("peak concurrency %d exceeds the 20-request cap", p)
	} else if p < 15 {
		t.Fatalf("peak concurrency %d suspiciously low — semaphore may be serializing", p)
	}
}
