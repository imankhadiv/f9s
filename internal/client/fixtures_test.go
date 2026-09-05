package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
)

// fixtureServer serves the recorded API responses of one Flink version.
func fixtureServer(t *testing.T, version string) (*Client, map[string]string) {
	t.Helper()
	dir := filepath.Join("testdata", "flink-"+version)
	metaRaw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatalf("fixtures for flink-%s missing: %v", version, err)
	}
	var meta map[string]string
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatal(err)
	}
	jid, vid, tmid := meta["jid"], meta["vid"], meta["tmid"]
	routes := map[string]string{
		"/overview":                                               "overview.json",
		"/jobs/overview":                                          "jobs-overview.json",
		"/jobs/" + jid:                                            "job.json",
		"/jobs/" + jid + "/config":                                "job-config.json",
		"/jobs/" + jid + "/plan":                                  "job-plan.json",
		"/jobs/" + jid + "/checkpoints":                           "checkpoints.json",
		"/jobs/" + jid + "/checkpoints/config":                    "checkpoint-config.json",
		"/jobs/" + jid + "/vertices/" + vid:                       "vertex.json",
		"/jobs/" + jid + "/vertices/" + vid + "/subtasks/metrics": "subtask-metrics.json",
		"/jobs/" + jid + "/exceptions":                            "exceptions.json",
		"/taskmanagers":                                           "taskmanagers.json",
		"/taskmanagers/" + tmid:                                   "taskmanager.json",
		"/taskmanagers/" + tmid + "/logs":                         "tm-logs.json",
		"/jobmanager/config":                                      "jm-config.json",
		"/jars":                                                   "jars.json",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unfixtured path requested: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return New(config.Context{URL: srv.URL}), meta
}

// Every major client method must parse each recorded Flink version without
// silently zero-valuing the fields views depend on.
func TestClientAgainstRecordedVersions(t *testing.T) {
	versions, _ := filepath.Glob(filepath.Join("testdata", "flink-*"))
	if len(versions) == 0 {
		t.Fatal("no recorded fixture versions")
	}
	for _, vdir := range versions {
		version := strings.TrimPrefix(filepath.Base(vdir), "flink-")
		t.Run(version, func(t *testing.T) {
			cl, meta := fixtureServer(t, version)
			jid, vid, tmid := meta["jid"], meta["vid"], meta["tmid"]

			ov, err := cl.Overview()
			if err != nil || ov.FlinkVersion == "" || ov.SlotsTotal == 0 {
				t.Fatalf("overview: %v %+v", err, ov)
			}
			jobs, err := cl.Jobs()
			if err != nil || len(jobs) == 0 || jobs[0].JID == "" || jobs[0].State == "" {
				t.Fatalf("jobs: %v", err)
			}
			job, err := cl.Job(jid)
			if err != nil || len(job.Vertices) == 0 || job.Vertices[0].Name == "" || job.Vertices[0].Parallelism == 0 {
				t.Fatalf("job: %v %+v", err, job)
			}
			jc, err := cl.JobConfig(jid)
			if err != nil || jc.ExecutionConfig.RestartStrategy == "" {
				t.Fatalf("job config: %v %+v", err, jc)
			}
			plan, err := cl.JobPlan(jid)
			if err != nil || len(plan.Nodes) == 0 || len(plan.Nodes[0].ID) == 0 {
				t.Fatalf("plan: %v", err)
			}
			cp, err := cl.Checkpoints(jid)
			if err != nil || cp.Counts.Total == 0 || cp.Summary.EndToEndDuration.Max == 0 {
				t.Fatalf("checkpoints (counts/summary): %v %+v", err, cp.Counts)
			}
			cc, err := cl.CheckpointConfig(jid)
			if err != nil || cc.Mode == "" || cc.Interval == 0 || cc.StateBackend == "" {
				t.Fatalf("checkpoint config: %v %+v", err, cc)
			}
			subs, err := cl.VertexSubtasks(jid, vid)
			if err != nil || len(subs) == 0 || subs[0].TaskManagerID == "" {
				t.Fatalf("subtasks: %v", err)
			}
			ms, err := cl.MetricValues("/jobs/"+jid+"/vertices/"+vid+"/subtasks/metrics", []string{"busyTimeMsPerSecond"})
			if err != nil || len(ms) == 0 {
				t.Fatalf("metrics: %v", err)
			}
			if _, ok := ms[0].Float(); !ok {
				t.Fatalf("aggregated metric must produce a value: %+v", ms[0])
			}
			tms, err := cl.TaskManagers()
			if err != nil || len(tms) == 0 || tms[0].SlotsNumber == 0 {
				t.Fatalf("taskmanagers: %v", err)
			}
			tm, err := cl.TaskManager(tmid)
			if err != nil || tm.MemoryConfiguration.TotalProcessMemory == 0 || tm.Metrics.HeapMax == 0 {
				t.Fatalf("taskmanager (memory model): %v", err)
			}
			files, err := cl.TMLogFiles(tmid)
			if err != nil || len(files) == 0 || files[0].Name == "" {
				t.Fatalf("tm log files: %v", err)
			}
			if _, err := cl.ListJars(); err != nil {
				t.Fatalf("jars: %v", err)
			}
			if _, err := cl.Exceptions(jid); err != nil {
				t.Fatalf("exceptions: %v", err)
			}
		})
	}
}
