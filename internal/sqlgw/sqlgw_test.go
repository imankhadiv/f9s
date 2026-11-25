package sqlgw

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOpenSessionAndExecute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/sessions":
			_, _ = w.Write([]byte(`{"sessionHandle":"s-1"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/sessions/s-1/statements":
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("content-type = %q", ct)
			}
			var body struct{ Statement string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Statement != "SELECT 1" {
				t.Errorf("statement = %q", body.Statement)
			}
			_, _ = w.Write([]byte(`{"operationHandle":"op-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// A trailing slash on the configured URL must not produce "//v2/...".
	c := New(srv.URL + "/")
	sess, err := c.OpenSession()
	if err != nil || sess != "s-1" {
		t.Fatalf("OpenSession = %q, %v", sess, err)
	}
	op, err := c.Execute(sess, "SELECT 1")
	if err != nil || op != "op-1" {
		t.Fatalf("Execute = %q, %v", op, err)
	}
}

func TestCallSurfacesGatewayError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "  session expired  ", http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Execute("gone", "SELECT 1")
	if err == nil {
		t.Fatal("want error on non-200")
	}
	for _, want := range []string{"POST", "/v2/sessions/gone/statements", "404", "session expired"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// fetchServer serves result pages in order and records whether the operation
// was closed.
func fetchServer(t *testing.T, pages []string, closed *bool) *httptest.Server {
	t.Helper()
	i := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			*closed = strings.HasSuffix(r.URL.Path, "/operations/op/close")
			return
		}
		if i >= len(pages) {
			t.Errorf("unexpected extra fetch %s", r.URL)
			http.Error(w, "extra", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(strings.ReplaceAll(pages[i], "$BASE", "http://"+r.Host)))
		i++
	}))
}

func TestFetchAllFollowsPagesAndFormatsChangelog(t *testing.T) {
	var closed bool
	srv := fetchServer(t, []string{
		`{"resultType":"PAYLOAD","results":{"columns":[{"name":"id"},{"name":"v"}],
		  "data":[{"kind":"INSERT","fields":[1,"a"]},{"kind":"UPDATE_AFTER","fields":[2,null]}]},
		  "nextResultUri":"/v2/sessions/s/operations/op/result/1?rowFormat=JSON"}`,
		// Absolute next URI, as some gateways return.
		`{"resultType":"PAYLOAD","results":{"columns":[{"name":"ignored"}],
		  "data":[{"kind":"DELETE","fields":[3,"c"]}]},
		  "nextResultUri":"$BASE/v2/sessions/s/operations/op/result/2?rowFormat=JSON"}`,
		`{"resultType":"EOS","results":{"columns":[],"data":[]}}`,
	}, &closed)
	defer srv.Close()

	cols, rows, err := New(srv.URL).FetchAll("s", "op", 100, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"id", "v"}; !reflect.DeepEqual(cols, want) {
		t.Errorf("cols = %v, want %v", cols, want)
	}
	want := [][]string{{"1", "a"}, {"UPDATE_AFTER 2", "<nil>"}, {"DELETE 3", "c"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %v, want %v", rows, want)
	}
	if !closed {
		t.Error("operation was not closed")
	}
}

func TestFetchAllStopsAtRowCap(t *testing.T) {
	var closed bool
	// One page only: hitting the cap must not trigger a second fetch even
	// though nextResultUri says the stream continues.
	srv := fetchServer(t, []string{
		`{"resultType":"PAYLOAD","results":{"columns":[{"name":"n"}],
		  "data":[{"kind":"INSERT","fields":[1]},{"kind":"INSERT","fields":[2]},{"kind":"INSERT","fields":[3]}]},
		  "nextResultUri":"/v2/sessions/s/operations/op/result/1?rowFormat=JSON"}`,
	}, &closed)
	defer srv.Close()

	_, rows, err := New(srv.URL).FetchAll("s", "op", 2, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want 2", rows)
	}
	if !closed {
		t.Error("operation was not closed")
	}
}

func TestFetchAllReturnsPartialRowsOnError(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			return
		}
		n++
		if n == 1 {
			_, _ = w.Write([]byte(`{"resultType":"PAYLOAD","results":{"columns":[{"name":"n"}],
			  "data":[{"kind":"INSERT","fields":[1]}]},"nextResultUri":"/v2/next"}`))
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cols, rows, err := New(srv.URL).FetchAll("s", "op", 10, 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want boom", err)
	}
	if len(cols) != 1 || len(rows) != 1 {
		t.Errorf("partial result lost: cols=%v rows=%v", cols, rows)
	}
}
