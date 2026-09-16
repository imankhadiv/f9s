package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

func TestMetricSelectorsPreserveReservedCharacters(t *testing.T) {
	type request struct {
		path     string
		rawQuery string
		selector string
	}
	requests := make(chan request, 3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- request{
			path:     r.URL.Path,
			rawQuery: r.URL.RawQuery,
			selector: r.URL.Query().Get("get"),
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	cl := New(config.Context{URL: srv.URL})

	keys := []string{
		"plain", "a_+_b", "a#b", "a$b", "a&b", "a/b", "a;b", "a=b", "a?b", "a@b",
	}
	wantSelector := strings.Join(keys, ",")
	// each id escaped on its own; the separating commas stay literal
	wantRawQuery := "get=plain,a_%2B_b,a%23b,a%24b,a%26b,a%2Fb,a%3Bb,a%3Db,a%3Fb,a%40b"

	if _, err := cl.JobMetrics("job-1", keys...); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.MetricValues("/jobmanager/metrics", keys); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.MetricValues("/taskmanagers/tm-1/metrics", keys); err != nil {
		t.Fatal(err)
	}

	for _, wantPath := range []string{
		"/jobs/job-1/metrics",
		"/jobmanager/metrics",
		"/taskmanagers/tm-1/metrics",
	} {
		got := <-requests
		if got.path != wantPath {
			t.Errorf("path = %q, want %q", got.path, wantPath)
		}
		if got.selector != wantSelector {
			t.Errorf("decoded selector = %q, want %q", got.selector, wantSelector)
		}
		if got.rawQuery != wantRawQuery {
			t.Errorf("raw query = %q, want %q", got.rawQuery, wantRawQuery)
		}
	}
}

func TestSubtaskMetricEncodesCustomMetricName(t *testing.T) {
	var rawQuery, selector string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		selector = r.URL.Query().Get("get")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)
	cl := New(config.Context{URL: srv.URL})

	if _, err := cl.SubtaskMetric("job-1", "vertex-1", "custom+/#&?", 2); err != nil {
		t.Fatal(err)
	}
	if selector != "0.custom+/#&?,1.custom+/#&?" {
		t.Fatalf("decoded selector = %q", selector)
	}
	if rawQuery != "get=0.custom%2B%2F%23%26%3F,1.custom%2B%2F%23%26%3F" {
		t.Fatalf("raw query = %q", rawQuery)
	}
}

// The comma between selectors is Flink's separator and is legal unencoded, so
// it must reach the wire literally. Percent-encoding it would alter every
// multi-metric read f9s makes — against every Flink version and any proxy in
// front of it — to fix a problem that only ever concerned the ids themselves.
func TestMetricSelectorSeparatorIsNotEncoded(t *testing.T) {
	if got := metricQuery([]string{"a", "b", "c"}); got != "?get=a,b,c" {
		t.Errorf("metricQuery = %q, want %q", got, "?get=a,b,c")
	}
	if got := metricQuery([]string{"numRestarts"}); got != "?get=numRestarts" {
		t.Errorf("single selector = %q", got)
	}
	// a comma inside an id is escaped, so it cannot be mistaken for a separator
	if got := metricQuery([]string{"a,b"}); got != "?get=a%2Cb" {
		t.Errorf("embedded comma = %q, want %q", got, "?get=a%2Cb")
	}
}
