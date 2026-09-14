package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

// fixtures mirror real Flink 1.20/2.x monitoring REST API payloads.
var fixtures = map[string]string{
	"/overview": `{"taskmanagers":2,"slots-total":8,"slots-available":5,
		"jobs-running":1,"jobs-finished":0,"jobs-cancelled":0,"jobs-failed":0,
		"flink-version":"2.1.0","flink-commit":"abc1234"}`,
	"/jobs/overview": `{"jobs":[{"jid":"a3f21c00000000000000000000000000",
		"name":"orders-enrichment","state":"RUNNING","start-time":1756300000000,
		"end-time":-1,"duration":1209600000,"last-modification":1756300000000,
		"tasks":{"total":8,"created":0,"scheduled":0,"deploying":0,"running":8,
		"finished":0,"canceling":0,"canceled":0,"failed":0,"reconciling":0,"initializing":0}}]}`,
	"/jobs/a3f21c00000000000000000000000000": `{"jid":"a3f21c00000000000000000000000000",
		"name":"orders-enrichment","state":"RUNNING","start-time":1756300000000,
		"duration":1209600000,"now":1757509600000,
		"vertices":[{"id":"v1","name":"Source: Kafka","parallelism":4,"status":"RUNNING",
		"start-time":1756300000000,"duration":1209600000,
		"tasks":{"total":4,"running":4,"failed":0},
		"metrics":{"read-bytes":0,"write-bytes":1048576,"read-records":0,"write-records":420000}}],
		"status-counts":{"RUNNING":1}}`,
	"/jobs/a3f21c00000000000000000000000000/checkpoints": `{
		"counts":{"restored":1,"total":320,"in_progress":0,"completed":318,"failed":2},
		"summary":{},
		"latest":{},
		"history":[{"id":320,"status":"COMPLETED","is_savepoint":false,
		"trigger_timestamp":1757509500000,"latest_ack_timestamp":1757509532000,
		"state_size":52428800,"end_to_end_duration":32000,"num_subtasks":8,
		"num_acknowledged_subtasks":8,"checkpoint_type":"CHECKPOINT",
		"external_path":"s3://bucket/ckpt-320"},
		{"id":319,"status":"FAILED","is_savepoint":false,
		"trigger_timestamp":1757509200000,"state_size":0,"end_to_end_duration":600000,
		"num_subtasks":8,"num_acknowledged_subtasks":5,
		"failure_message":"Checkpoint expired before completing."}]}`,
	"/jobs/a3f21c00000000000000000000000000/exceptions": `{
		"root-exception":"java.lang.RuntimeException: partition lost",
		"timestamp":1757509000000,"truncated":false,
		"all-exceptions":[],
		"exceptionHistory":{"entries":[{"exceptionName":"java.lang.RuntimeException",
		"stacktrace":"java.lang.RuntimeException: partition lost\n\tat org.x.Y(Z.java:1)",
		"timestamp":1757509000000,"taskName":"Source: Kafka (3/4)",
		"location":"10.0.0.7:34567"}],"truncated":false}}`,
	"/jobs/a3f21c00000000000000000000000000/vertices/v1/backpressure": `{
		"status":"ok","backpressureLevel":"low","end-timestamp":1757509600000,
		"subtasks":[{"subtask":0,"backpressureLevel":"low","ratio":0.1}]}`,
}

func TestJarPlanUsesPostWithJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/jars/job.jar/plan" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 0 {
			t.Fatalf("body = %#v, want empty object", body)
		}
		_, _ = w.Write([]byte(`{"plan":{"nodes":[{"id":"source"}]}}`))
	}))
	defer srv.Close()

	plan, err := New(config.Context{URL: srv.URL}).JarPlan("job.jar")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 1 || plan.Nodes[0].ID != "source" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestRunJarRestoreOptions(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/jars/job.jar/run" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		_, _ = w.Write([]byte(`{"jobid":"new-job"}`))
	}))
	defer srv.Close()
	cl := New(config.Context{URL: srv.URL})

	cases := []struct {
		name string
		opts RunJarOptions
		want map[string]any
	}{
		{name: "fresh run", want: map[string]any{}},
		{name: "strict restore", opts: RunJarOptions{SavepointPath: "s3://state/sp"}, want: map[string]any{
			"savepointPath": "s3://state/sp", "allowNonRestoredState": false,
		}},
		{name: "explicit unsafe restore", opts: RunJarOptions{SavepointPath: "s3://state/sp", AllowNonRestoredState: true}, want: map[string]any{
			"savepointPath": "s3://state/sp", "allowNonRestoredState": true,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jid, err := cl.RunJar("job.jar", tc.opts)
			if err != nil || jid != "new-job" {
				t.Fatalf("jid=%q err=%v", jid, err)
			}
			got := bodies[len(bodies)-1]
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("body = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func testClient(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := fixtures[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(config.Context{URL: srv.URL})
}

func TestOverview(t *testing.T) {
	ov, err := testClient(t).Overview()
	if err != nil {
		t.Fatal(err)
	}
	if ov.FlinkVersion != "2.1.0" || ov.SlotsTotal != 8 || ov.TaskManagers != 2 {
		t.Fatalf("unexpected overview: %+v", ov)
	}
}

func TestJobs(t *testing.T) {
	jobs, err := testClient(t).Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	j := jobs[0]
	if j.Name != "orders-enrichment" || j.State != "RUNNING" || j.Tasks.Running != 8 {
		t.Fatalf("unexpected job: %+v", j)
	}
}

func TestJobDetail(t *testing.T) {
	job, err := testClient(t).Job("a3f21c00000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if len(job.Vertices) != 1 || job.Vertices[0].Parallelism != 4 {
		t.Fatalf("unexpected vertices: %+v", job.Vertices)
	}
	if job.Vertices[0].Metrics.WriteRecords != 420000 {
		t.Fatalf("unexpected metrics: %+v", job.Vertices[0].Metrics)
	}
}

func TestCheckpoints(t *testing.T) {
	cp, err := testClient(t).Checkpoints("a3f21c00000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if cp.Counts.Completed != 318 || cp.Counts.Failed != 2 {
		t.Fatalf("unexpected counts: %+v", cp.Counts)
	}
	if len(cp.History) != 2 || cp.History[1].FailureMessage == "" {
		t.Fatalf("unexpected history: %+v", cp.History)
	}
	if cp.History[0].ExternalPath != "s3://bucket/ckpt-320" {
		t.Fatalf("external path lost: %+v", cp.History[0])
	}
}

func TestExceptions(t *testing.T) {
	ex, err := testClient(t).Exceptions("a3f21c00000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if ex.RootException == "" || len(ex.History.Entries) != 1 {
		t.Fatalf("unexpected exceptions: %+v", ex)
	}
}

func TestBackpressureLevelNormalization(t *testing.T) {
	bp, err := testClient(t).Backpressure("a3f21c00000000000000000000000000", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if bp.Level() != "low" {
		t.Fatalf("want level low, got %q", bp.Level())
	}
}

func actionServer(t *testing.T) (*Client, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/jobs/j1/savepoints":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["target-directory"] != "s3://b/sp" {
				t.Errorf("bad savepoint body: %v", body)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"request-id":"req-1"}`))
		case r.Method == "POST" && r.URL.Path == "/jobs/j1/stop":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"request-id":"req-2"}`))
		case r.Method == "GET" && r.URL.Path == "/jobs/j1/savepoints/req-1":
			_, _ = w.Write([]byte(`{"status":{"id":"COMPLETED"},"operation":{"location":"s3://b/sp/savepoint-abc"}}`))
		case r.Method == "PATCH" && r.URL.Path == "/jobs/j1":
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return New(config.Context{URL: srv.URL}), &calls
}

func TestTriggerSavepoint(t *testing.T) {
	cl, _ := actionServer(t)
	id, err := cl.TriggerSavepoint("j1", "s3://b/sp")
	if err != nil || id != "req-1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestStopWithSavepoint(t *testing.T) {
	cl, _ := actionServer(t)
	id, err := cl.StopWithSavepoint("j1", "", false)
	if err != nil || id != "req-2" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestSavepointStatus(t *testing.T) {
	cl, _ := actionServer(t)
	st, err := cl.SavepointStatus("j1", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if st.Status.ID != "COMPLETED" || st.Failed() || st.Operation.Location == "" {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestCancelJob(t *testing.T) {
	cl, calls := actionServer(t)
	if err := cl.CancelJob("j1"); err != nil {
		t.Fatal(err)
	}
	last := (*calls)[len(*calls)-1]
	if last != "PATCH /jobs/j1?mode=cancel" {
		t.Fatalf("unexpected call: %s", last)
	}
}

func TestErrorOnNon200(t *testing.T) {
	if _, err := testClient(t).Job("does-not-exist"); err == nil {
		t.Fatal("want error for missing job, got nil")
	}
}
