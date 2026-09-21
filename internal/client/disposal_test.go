package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

func TestDisposeSavepointPostsPathAndPolls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/savepoint-disposal":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["savepoint-path"] != "s3://sp/savepoint-1" {
				t.Fatalf("body = %v %v", body, err)
			}
			_, _ = w.Write([]byte(`{"request-id":"req-7"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/savepoint-disposal/req-7":
			_, _ = w.Write([]byte(`{"status":{"id":"COMPLETED"},"operation":{}}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	cl := New(config.Context{URL: srv.URL})
	id, err := cl.DisposeSavepoint("s3://sp/savepoint-1")
	if err != nil || id != "req-7" {
		t.Fatalf("dispose = %q %v", id, err)
	}
	st, err := cl.DisposalStatus(id)
	if err != nil || st.Status.ID != "COMPLETED" || st.Operation.FailureCause.Class != "" {
		t.Fatalf("status = %+v %v", st, err)
	}
}
