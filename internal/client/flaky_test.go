package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

// A degraded cluster must produce classified, hint-carrying errors — never
// raw decode noise or silent zero values.
func TestDegradedClusterClassification(t *testing.T) {
	t.Run("truncated body is a schema error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"jobs":[{"jid":"abc","name":"trunca`)) // cut mid-value
		}))
		defer srv.Close()
		_, err := New(config.Context{URL: srv.URL}).Jobs()
		ce, ok := err.(*ClientError)
		if !ok || ce.Kind != KindSchema {
			t.Fatalf("want schema classification, got %T %v", err, err)
		}
	})
	t.Run("503 keeps the envelope's human line", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(503)
			w.Write([]byte(`{"errors":["org.apache.flink.runtime.rest.NotReadyException: JobManager is initializing\n\tat x"]}`))
		}))
		defer srv.Close()
		_, err := New(config.Context{URL: srv.URL}).Jobs()
		ce, ok := err.(*ClientError)
		if !ok || ce.Kind != KindServer || !strings.Contains(ce.Error(), "JobManager is initializing") {
			t.Fatalf("want server error with human line, got %v", err)
		}
	})
}
