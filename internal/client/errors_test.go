package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

func serve(code int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
}

func kindOf(t *testing.T, err error) *ClientError {
	t.Helper()
	ce, ok := err.(*ClientError)
	if !ok {
		t.Fatalf("want *ClientError, got %T: %v", err, err)
	}
	return ce
}

func TestErrorTaxonomy(t *testing.T) {
	t.Run("auth", func(t *testing.T) {
		srv := serve(401, `{}`)
		defer srv.Close()
		_, err := New(config.Context{URL: srv.URL}).Jobs()
		ce := kindOf(t, err)
		if ce.Kind != KindAuth || !strings.Contains(ce.Hint, "auth") {
			t.Fatalf("want auth hint, got %v", ce)
		}
	})
	t.Run("not-found carries flink version", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/overview", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"flink-version":"1.14.0"}`))
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
		srv := httptest.NewServer(mux)
		defer srv.Close()
		cl := New(config.Context{URL: srv.URL})
		_, _ = cl.Overview()
		_, err := cl.Jobs()
		ce := kindOf(t, err)
		if ce.Kind != KindNotFound || !strings.Contains(ce.Hint, "1.14.0") {
			t.Fatalf("want version-aware not-found hint, got %v", ce)
		}
	})
	t.Run("server envelope surfaces first line", func(t *testing.T) {
		srv := serve(500, `{"errors":["org.apache.flink.runtime.rest.handler.RestHandlerException: Invalid attempt num 3\n\tat stack"]}`)
		defer srv.Close()
		_, err := New(config.Context{URL: srv.URL}).Jobs()
		ce := kindOf(t, err)
		if ce.Kind != KindServer || !strings.Contains(ce.Error(), "Invalid attempt num 3") || strings.Contains(ce.Error(), "at stack") {
			t.Fatalf("want first envelope line without stack, got %v", ce)
		}
	})
	t.Run("schema mismatch on bad shape", func(t *testing.T) {
		srv := serve(200, `["not","an","object"]`)
		defer srv.Close()
		_, err := New(config.Context{URL: srv.URL}).Job("x")
		ce := kindOf(t, err)
		if ce.Kind != KindSchema {
			t.Fatalf("want schema kind, got %v", ce)
		}
	})
	t.Run("network refused", func(t *testing.T) {
		_, err := New(config.Context{URL: "http://127.0.0.1:1"}).Jobs()
		ce := kindOf(t, err)
		if ce.Kind != KindNetwork || !strings.Contains(ce.Hint, "reach the cluster") {
			t.Fatalf("want network hint, got %v", ce)
		}
	})
}
