// Package client is a minimal typed client for Flink's monitoring REST API
// (the same API the Flink Web UI uses). Every call carries a hard timeout so
// a hung JobManager can never freeze the UI.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/model"
)

const requestTimeout = 5 * time.Second

// CallRecord is one REST call f9s made — the lazygit command-log idea
// applied to HTTP (DESIGN.md #6): trust through transparency.
type CallRecord struct {
	Time     time.Time
	Method   string
	Path     string
	Status   string
	Duration time.Duration
}

var (
	callMu   sync.Mutex
	calls    [100]CallRecord
	callN    int
	callSeen int
)

func record(method, path, status string, start time.Time) {
	callMu.Lock()
	calls[callN%len(calls)] = CallRecord{Time: start, Method: method, Path: path, Status: status, Duration: time.Since(start)}
	callN++
	if callSeen < len(calls) {
		callSeen++
	}
	callMu.Unlock()
}

// RecentCalls returns the last recorded REST calls, newest first.
func RecentCalls() []CallRecord {
	callMu.Lock()
	defer callMu.Unlock()
	out := make([]CallRecord, 0, callSeen)
	for i := 1; i <= callSeen; i++ {
		out = append(out, calls[(callN-i+len(calls))%len(calls)])
	}
	return out
}

// Client talks to one Flink cluster.
type Client struct {
	versionMu sync.Mutex
	version   string // flink-version cached from the last Overview
	modeMu    sync.Mutex
	mode      *model.ModeInfo // deployment mode, cached (config is near-static)
	base      string
	hc        *http.Client
	header    http.Header
}

// New builds a client for the given cluster context.
func New(ctx config.Context) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if ctx.TLSSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- explicit per-context opt-in
	}
	header := http.Header{}
	for k, v := range ctx.Headers {
		header.Set(k, v)
	}
	switch ctx.Auth.Type {
	case "bearer":
		if tok := ctx.Auth.BearerToken(); tok != "" {
			header.Set("Authorization", "Bearer "+tok)
		}
	case "basic":
		cred := ctx.Auth.User + ":" + os.Getenv(ctx.Auth.PassEnv)
		header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cred)))
	}
	return &Client{
		base:   strings.TrimRight(ctx.URL, "/"),
		hc:     &http.Client{Transport: transport, Timeout: requestTimeout},
		header: header,
	}
}

// reqSem caps concurrent REST calls process-wide, so refresh fan-outs on a
// big cluster can't storm a struggling JobManager (a monitoring tool must
// never become part of the outage).
var reqSem = make(chan struct{}, 20)

func acquireSlot() func() {
	reqSem <- struct{}{}
	return func() { <-reqSem }
}

// FlinkVersion returns the cluster version cached from the last Overview.
func (c *Client) FlinkVersion() string {
	c.versionMu.Lock()
	defer c.versionMu.Unlock()
	return c.version
}

func (c *Client) setVersion(v string) {
	if v == "" {
		return
	}
	c.versionMu.Lock()
	c.version = v
	c.versionMu.Unlock()
}

func (c *Client) get(path string, out any) error {
	defer acquireSlot()()
	start := time.Now()
	status := "network error"
	defer func() { record(http.MethodGet, path, status, start) }()
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	op := "GET " + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	for k, vs := range c.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return classifyNetErr(op, err)
	}
	defer resp.Body.Close()
	status = resp.Status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return c.classifyStatus(op, resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return classifyDecode(op, err)
	}
	return nil
}

func (c *Client) send(method, path string, body, out any) error {
	defer acquireSlot()()
	start := time.Now()
	status := "network error"
	defer func() { record(method, path, status, start) }()
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range c.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return classifyNetErr(method+" "+path, err)
	}
	defer resp.Body.Close()
	status = resp.Status
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return c.classifyStatus(method+" "+path, resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Jar is one uploaded jar on GET /jars.
type Jar struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Uploaded int64  `json:"uploaded"`
	Entry    []struct {
		Name string `json:"name"`
	} `json:"entry"`
}

// ListJars is GET /jars.
func (c *Client) ListJars() ([]Jar, error) {
	var v struct {
		Files []Jar `json:"files"`
	}
	err := c.get("/jars", &v)
	return v.Files, err
}

// UploadJar is POST /jars/upload (multipart). Returns the server-side jar id.
func (c *Client) UploadJar(localPath string) (string, error) {
	start := time.Now()
	status := "network error"
	defer func() { record(http.MethodPost, "/jars/upload", status, start) }()
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="jarfile"; filename="%s"`, filepath.Base(localPath)))
	h.Set("Content-Type", "application/x-java-archive")
	part, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	mw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/jars/upload", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for k, vs := range c.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	status = resp.Status
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("upload %s: %s %s", filepath.Base(localPath), resp.Status, strings.TrimSpace(string(msg)))
	}
	var v struct {
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	return filepath.Base(v.Filename), nil
}

// JarPlan is POST /jars/:id/plan — the topology the jar would deploy.
func (c *Client) JarPlan(jarID string) (model.Plan, error) {
	var v struct {
		Plan model.Plan `json:"plan"`
	}
	err := c.send(http.MethodPost, "/jars/"+jarID+"/plan", map[string]any{}, &v)
	return v.Plan, err
}

// RunJarOptions controls a jar submission. AllowNonRestoredState is unsafe:
// Flink may discard savepoint state that cannot be mapped to the submitted job.
type RunJarOptions struct {
	SavepointPath         string
	AllowNonRestoredState bool
}

// RunJar is POST /jars/:id/run — submits a job, optionally restoring from a
// savepoint. Returns the new job id.
func (c *Client) RunJar(jarID string, opts RunJarOptions) (string, error) {
	body := map[string]any{}
	if opts.SavepointPath != "" {
		body["savepointPath"] = opts.SavepointPath
		body["allowNonRestoredState"] = opts.AllowNonRestoredState
	}
	var v struct {
		JobID string `json:"jobid"`
	}
	err := c.send(http.MethodPost, "/jars/"+jarID+"/run", body, &v)
	return v.JobID, err
}

// DeleteJar is DELETE /jars/:id.
func (c *Client) DeleteJar(jarID string) error {
	return c.send(http.MethodDelete, "/jars/"+jarID, nil, nil)
}

// TriggerSavepoint is POST /jobs/:jid/savepoints; returns the trigger id to
// poll with SavepointStatus. An empty dir uses the cluster's default
// state.savepoints.dir.
func (c *Client) TriggerSavepoint(jid, dir string) (string, error) {
	body := map[string]any{"cancel-job": false}
	if dir != "" {
		body["target-directory"] = dir
	}
	var v model.TriggerResponse
	err := c.send(http.MethodPost, "/jobs/"+jid+"/savepoints", body, &v)
	return v.RequestID, err
}

// StopWithSavepoint is POST /jobs/:jid/stop: take a savepoint, then stop the
// job gracefully. Poll the returned trigger id with SavepointStatus.
func (c *Client) StopWithSavepoint(jid, dir string, drain bool) (string, error) {
	body := map[string]any{"drain": drain}
	if dir != "" {
		body["targetDirectory"] = dir
	}
	var v model.TriggerResponse
	err := c.send(http.MethodPost, "/jobs/"+jid+"/stop", body, &v)
	return v.RequestID, err
}

// SavepointStatus is GET /jobs/:jid/savepoints/:triggerid.
func (c *Client) SavepointStatus(jid, reqID string) (model.SavepointStatus, error) {
	var v model.SavepointStatus
	err := c.get("/jobs/"+jid+"/savepoints/"+reqID, &v)
	return v, err
}

// CancelJob is PATCH /jobs/:jid?mode=cancel.
func (c *Client) CancelJob(jid string) error {
	return c.send(http.MethodPatch, "/jobs/"+jid+"?mode=cancel", nil, nil)
}

// Overview is GET /overview.
func (c *Client) Overview() (model.ClusterOverview, error) {
	var v model.ClusterOverview
	err := c.get("/overview", &v)
	if err == nil {
		c.setVersion(v.FlinkVersion)
	}
	return v, err
}

// Jobs is GET /jobs/overview.
func (c *Client) Jobs() ([]model.JobOverview, error) {
	var v model.JobsOverview
	err := c.get("/jobs/overview", &v)
	return v.Jobs, err
}

// Job is GET /jobs/:jid.
func (c *Client) Job(jid string) (model.Job, error) {
	var v model.Job
	err := c.get("/jobs/"+jid, &v)
	return v, err
}

// JobPlan is GET /jobs/:jid/plan — the execution graph topology.
func (c *Client) JobPlan(jid string) (model.Plan, error) {
	var v struct {
		Plan model.Plan `json:"plan"`
	}
	err := c.get("/jobs/"+jid+"/plan", &v)
	return v.Plan, err
}

// Checkpoints is GET /jobs/:jid/checkpoints.
func (c *Client) Checkpoints(jid string) (model.Checkpoints, error) {
	var v model.Checkpoints
	err := c.get("/jobs/"+jid+"/checkpoints", &v)
	return v, err
}

// CheckpointConfig is GET /jobs/:jid/checkpoints/config.
func (c *Client) CheckpointConfig(jid string) (model.CheckpointConfig, error) {
	var v model.CheckpointConfig
	err := c.get("/jobs/"+jid+"/checkpoints/config", &v)
	return v, err
}

// CheckpointSubtasks is GET /jobs/:jid/checkpoints/details/:cp/subtasks/:vid.
func (c *Client) CheckpointSubtasks(jid string, cpID int64, vid string) ([]model.CheckpointSubtask, error) {
	var v struct {
		Subtasks []model.CheckpointSubtask `json:"subtasks"`
	}
	err := c.get(fmt.Sprintf("/jobs/%s/checkpoints/details/%d/subtasks/%s", jid, cpID, vid), &v)
	return v.Subtasks, err
}

// Exceptions is GET /jobs/:jid/exceptions.
func (c *Client) Exceptions(jid string) (model.Exceptions, error) {
	var v model.Exceptions
	err := c.get("/jobs/"+jid+"/exceptions?maxExceptions=20", &v)
	return v, err
}

// JobMetrics is GET /jobs/:jid/metrics?get=..., e.g. numRestarts.
func (c *Client) JobMetrics(jid string, keys ...string) ([]model.Metric, error) {
	var v []model.Metric
	err := c.getMetrics("/jobs/"+jid+"/metrics", keys, &v)
	return v, err
}

// metricQuery builds the ?get= selector for Flink's metric endpoints. Flink
// documents that metric names may contain reserved characters (# $ & + / ; = ?
// @), so each id is escaped on its own — but the commas between them are left
// literal. They are the separator Flink splits the decoded value on, they are
// legal unencoded in a query (RFC 3986 sub-delim), and that is what f9s has
// always put on the wire; escaping them would change every multi-metric read
// for no benefit.
func metricQuery(keys []string) string {
	escaped := make([]string, len(keys))
	for i, k := range keys {
		escaped[i] = url.QueryEscape(k)
	}
	return "?get=" + strings.Join(escaped, ",")
}

func (c *Client) getMetrics(path string, keys []string, out any) error {
	return c.get(path+metricQuery(keys), out)
}

// getText fetches a plain-text endpoint (log files), capped to 2MiB.
func (c *Client) getText(path string) (string, error) {
	defer acquireSlot()()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return "", err
	}
	for k, vs := range c.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return string(data), err
}

// TaskManagers is GET /taskmanagers.
func (c *Client) TaskManagers() ([]model.TaskManager, error) {
	var v model.TaskManagers
	err := c.get("/taskmanagers", &v)
	return v.TaskManagers, err
}

// TaskManager is GET /taskmanagers/:id (adds heap and GC metrics).
func (c *Client) TaskManager(id string) (model.TMDetail, error) {
	var v model.TMDetail
	err := c.get("/taskmanagers/"+id, &v)
	return v, err
}

// StartTMProfiler triggers async-profiler on a TaskManager
// (rest.profiling.enabled required; Flink 1.19+). ITIMER works where
// container perf events are blocked.
func (c *Client) StartTMProfiler(tmID, mode string, durationSec int) (model.ProfilingInfo, error) {
	var v model.ProfilingInfo
	err := c.send(http.MethodPost, "/taskmanagers/"+tmID+"/profiler",
		map[string]any{"mode": mode, "duration": durationSec}, &v)
	return v, err
}

// ListTMProfiles is GET /taskmanagers/:id/profiler, newest first.
func (c *Client) ListTMProfiles(tmID string) ([]model.ProfilingInfo, error) {
	var v struct {
		ProfilingList []model.ProfilingInfo `json:"profilingList"`
	}
	err := c.get("/taskmanagers/"+tmID+"/profiler", &v)
	return v.ProfilingList, err
}

// DownloadTMProfile fetches a finished flame-graph HTML.
func (c *Client) DownloadTMProfile(tmID, file string) (string, error) {
	return c.getText("/taskmanagers/" + tmID + "/profiler/" + file)
}

// TMThreadDump is GET /taskmanagers/:id/thread-dump.
func (c *Client) TMThreadDump(id string) (string, error) {
	var v struct {
		ThreadInfos []struct {
			ThreadName string `json:"threadName"`
			Info       string `json:"stringifiedThreadInfo"`
		} `json:"threadInfos"`
	}
	if err := c.get("/taskmanagers/"+id+"/thread-dump", &v); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, t := range v.ThreadInfos {
		b.WriteString(t.Info)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// TMLog is GET /taskmanagers/:id/log (plain text, capped).
func (c *Client) TMLog(id string) (string, error) {
	return c.getText("/taskmanagers/" + id + "/log")
}

// VertexMailbox returns the vertex's worst-subtask mailbox p99 latency (ms)
// and soft-backpressure rate (ms/s) — buffers filling before hard
// backpressure shows anywhere else.
func (c *Client) VertexMailbox(jid, vid string) (p99, softBP, debloatMs float64, err error) {
	ms, err := c.MetricValues("/jobs/"+jid+"/vertices/"+vid+"/subtasks/metrics", []string{
		"mailboxLatencyMs_p99", "softBackPressuredTimeMsPerSecond", "estimatedTimeToConsumeBuffersMs"})
	if err != nil {
		return -1, -1, -1, err
	}
	p99, softBP, debloatMs = -1, -1, -1
	for _, m := range ms {
		if m.Max == nil {
			continue
		}
		switch m.ID {
		case "mailboxLatencyMs_p99":
			p99 = *m.Max
		case "softBackPressuredTimeMsPerSecond":
			softBP = *m.Max
		case "estimatedTimeToConsumeBuffersMs":
			debloatMs = *m.Max // absent unless buffer debloating is enabled
			if debloatMs < 0 {
				debloatMs = -1 // sources report Long.MIN_VALUE (no input gate)
			}
		}
	}
	return p99, softBP, debloatMs, nil
}

// JobTaskManagers returns the distinct TaskManager ids currently running
// the job's subtasks (via taskmanager-id on each vertex's subtask list).
func (c *Client) JobTaskManagers(jid string) ([]string, error) {
	job, err := c.Job(jid)
	if err != nil {
		return nil, err
	}
	type vres struct{ ids []string }
	results := make([]vres, len(job.Vertices))
	var wg sync.WaitGroup
	for i, v := range job.Vertices {
		wg.Add(1)
		go func(i int, vid string) {
			defer wg.Done()
			var vd struct {
				Subtasks []struct {
					TaskManagerID string `json:"taskmanager-id"`
				} `json:"subtasks"`
			}
			if err := c.get("/jobs/"+jid+"/vertices/"+vid, &vd); err != nil {
				return
			}
			for _, st := range vd.Subtasks {
				results[i].ids = append(results[i].ids, st.TaskManagerID)
			}
		}(i, v.ID)
	}
	wg.Wait()
	seen := map[string]bool{}
	var out []string
	for _, r := range results {
		for _, id := range r.ids {
			if id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out, nil
}

// TMMemoryLive fetches the live memory-model metrics for one TaskManager.
func (c *Client) TMMemoryLive(id string) (model.TMMemoryLive, error) {
	keys := []string{
		"Status.Flink.Memory.Managed.Used", "Status.Flink.Memory.Managed.Total",
		"Status.Shuffle.Netty.AvailableMemorySegments", "Status.Shuffle.Netty.TotalMemorySegments",
		"Status.JVM.Memory.Metaspace.Used", "Status.JVM.Memory.Metaspace.Max",
	}
	ms, err := c.MetricValues("/taskmanagers/"+id+"/metrics", keys)
	var out model.TMMemoryLive
	if err != nil {
		return out, err
	}
	for _, m := range ms {
		v, ok := m.Float()
		if !ok {
			continue
		}
		switch m.ID {
		case "Status.Flink.Memory.Managed.Used":
			out.ManagedUsed = int64(v)
		case "Status.Flink.Memory.Managed.Total":
			out.ManagedTotal = int64(v)
		case "Status.Shuffle.Netty.AvailableMemorySegments":
			out.NetworkAvail = int64(v)
		case "Status.Shuffle.Netty.TotalMemorySegments":
			out.NetworkTotal = int64(v)
		case "Status.JVM.Memory.Metaspace.Used":
			out.MetaspaceUsed = int64(v)
		case "Status.JVM.Memory.Metaspace.Max":
			out.MetaspaceMax = int64(v)
		}
	}
	return out, nil
}

// JMLog is GET /jobmanager/log — the JobManager's log file.
func (c *Client) JMLog() (string, error) {
	return c.getText("/jobmanager/log")
}

// VertexSubtasks returns the vertex's per-subtask rows (current attempts).
func (c *Client) VertexSubtasks(jid, vid string) ([]model.SubtaskInfo, error) {
	var v struct {
		Subtasks []model.SubtaskInfo `json:"subtasks"`
	}
	err := c.get("/jobs/"+jid+"/vertices/"+vid, &v)
	return v.Subtasks, err
}

// SubtaskAttempt fetches one prior attempt of a subtask; clusters retain
// prior attempts only within the current execution-graph generation, so a
// missing attempt is normal, not exceptional.
func (c *Client) SubtaskAttempt(jid, vid string, subtask, attempt int) (model.SubtaskInfo, error) {
	var v model.SubtaskInfo
	err := c.get(fmt.Sprintf("/jobs/%s/vertices/%s/subtasks/%d/attempts/%d", jid, vid, subtask, attempt), &v)
	return v, err
}

// VertexInRates returns the vertex's total input rates (records/s and
// bytes/s summed across subtasks) — the throughput of the shuffle feeding
// it, i.e. its inbound DAG edge.
func (c *Client) VertexInRates(jid, vid string) (recs, bytes float64, err error) {
	return c.vertexRate(jid, vid, "In")
}

// VertexOutRates is the vertex's total output rate — for source vertices
// (which have no input) this is the pipeline's ingest throughput.
func (c *Client) VertexOutRates(jid, vid string) (recs, bytes float64, err error) {
	return c.vertexRate(jid, vid, "Out")
}

func (c *Client) vertexRate(jid, vid, dir string) (recs, bytes float64, err error) {
	ms, err := c.MetricValues("/jobs/"+jid+"/vertices/"+vid+"/subtasks/metrics",
		[]string{"numRecords" + dir + "PerSecond", "numBytes" + dir + "PerSecond"})
	if err != nil {
		return -1, -1, err
	}
	recs, bytes = -1, -1
	for _, m := range ms {
		if m.Sum == nil {
			continue
		}
		switch m.ID {
		case "numRecords" + dir + "PerSecond":
			recs = *m.Sum
		case "numBytes" + dir + "PerSecond":
			bytes = *m.Sum
		}
	}
	return recs, bytes, nil
}

// JobSlotCounts tallies how many task slots each job currently holds, by
// scanning every TaskManager's allocatedSlots — the basis for per-job
// utilization/waste accounting.
func (c *Client) JobSlotCounts() (map[string]int, error) {
	tms, err := c.TaskManagers()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, tm := range tms {
		d, err := c.TaskManager(tm.ID)
		if err != nil {
			continue
		}
		for _, sl := range d.AllocatedSlots {
			if sl.JobID != "" {
				counts[sl.JobID]++
			}
		}
	}
	return counts, nil
}

// JobConfig is GET /jobs/:jid/config — the job's execution config.
func (c *Client) JobConfig(jid string) (model.JobConfig, error) {
	var v model.JobConfig
	err := c.get("/jobs/"+jid+"/config", &v)
	return v, err
}

// TMLogFiles lists the TaskManager's rotated log files.
func (c *Client) TMLogFiles(id string) ([]model.LogFile, error) {
	var v struct {
		Logs []model.LogFile `json:"logs"`
	}
	err := c.get("/taskmanagers/"+id+"/logs", &v)
	return v.Logs, err
}

// JMLogFiles lists the JobManager's rotated log files.
func (c *Client) JMLogFiles() ([]model.LogFile, error) {
	var v struct {
		Logs []model.LogFile `json:"logs"`
	}
	err := c.get("/jobmanager/logs", &v)
	return v.Logs, err
}

// TMLogFile fetches one named TaskManager log file.
func (c *Client) TMLogFile(id, name string) (string, error) {
	return c.getText("/taskmanagers/" + id + "/logs/" + name)
}

// JMLogFile fetches one named JobManager log file.
func (c *Client) JMLogFile(name string) (string, error) {
	return c.getText("/jobmanager/logs/" + name)
}

// TMStdout tails the TaskManager's stdout (may not exist in containers —
// the server's message is returned as text).
func (c *Client) TMStdout(id string) (string, error) {
	return c.getText("/taskmanagers/" + id + "/stdout")
}

// JMStdout tails the JobManager's stdout.
func (c *Client) JMStdout() (string, error) {
	return c.getText("/jobmanager/stdout")
}

// SubtaskMetric fetches one per-subtask metric series, indexed by subtask.
func (c *Client) SubtaskMetric(jid, vid, metric string, parallelism int) ([]float64, error) {
	return c.subtaskMetric(jid, vid, metric, parallelism)
}

// subtaskMetric fetches one per-subtask metric series, indexed by subtask.
func (c *Client) subtaskMetric(jid, vid, metric string, parallelism int) ([]float64, error) {
	keys := make([]string, parallelism)
	for i := range keys {
		keys[i] = fmt.Sprintf("%d.%s", i, metric)
	}
	var ms []model.Metric
	err := c.getMetrics("/jobs/"+jid+"/vertices/"+vid+"/metrics", keys, &ms)
	if err != nil {
		return nil, err
	}
	out := make([]float64, parallelism)
	for _, m := range ms {
		var idx int
		var val float64
		if _, err := fmt.Sscanf(m.ID, "%d.", &idx); err != nil || idx >= parallelism {
			continue
		}
		if _, err := fmt.Sscanf(m.Value, "%g", &val); err != nil {
			continue
		}
		out[idx] = val
	}
	return out, nil
}

// VertexSubtaskBusy returns per-subtask busyTimeMsPerSecond.
func (c *Client) VertexSubtaskBusy(jid, vid string, parallelism int) ([]float64, error) {
	if parallelism < 2 {
		return nil, nil
	}
	return c.subtaskMetric(jid, vid, "busyTimeMsPerSecond", parallelism)
}

// VertexSubtaskRecords returns per-subtask record rates for a vertex (the
// skew signal), indexed by subtask. Sources have no input records, so when
// records-in is all zero the records-out distribution is used instead.
func (c *Client) VertexSubtaskRecords(jid, vid string, parallelism int) ([]float64, error) {
	if parallelism < 2 {
		return nil, nil
	}
	keys := make([]string, 0, parallelism*2)
	for i := 0; i < parallelism; i++ {
		keys = append(keys,
			fmt.Sprintf("%d.numRecordsInPerSecond", i),
			fmt.Sprintf("%d.numRecordsOutPerSecond", i))
	}
	var ms []model.Metric
	err := c.getMetrics("/jobs/"+jid+"/vertices/"+vid+"/metrics", keys, &ms)
	if err != nil {
		return nil, err
	}
	in := make([]float64, parallelism)
	outR := make([]float64, parallelism)
	inSum := 0.0
	for _, m := range ms {
		var idx int
		var val float64
		if _, err := fmt.Sscanf(m.Value, "%g", &val); err != nil {
			continue
		}
		if _, err := fmt.Sscanf(m.ID, "%d.numRecordsInPerSecond", &idx); err == nil && idx < parallelism {
			in[idx] = val
			inSum += val
			continue
		}
		if _, err := fmt.Sscanf(m.ID, "%d.numRecordsOutPerSecond", &idx); err == nil && idx < parallelism {
			outR[idx] = val
		}
	}
	if inSum > 0 {
		return in, nil
	}
	return outR, nil
}

// SubtaskPressure is the per-subtask backpressure fingerprint for one vertex:
// the busy/backpressured/idle time trio (0..1000 ms/s, they roughly sum to a
// second) plus the network buffer-pool usages (0..1). The pools pinpoint the
// TRUE backpressure edge that the coarse per-vertex level cannot — outPool→1
// means this subtask's output is blocked (downstream slow); inPool→1 means its
// input is starved (upstream slow). Each slice is indexed by subtask.
type SubtaskPressure struct {
	Busy          []float64
	Backpressured []float64
	Idle          []float64
	InPool        []float64
	OutPool       []float64
}

// VertexSubtaskPressure fetches the backpressure fingerprint for every subtask
// of a vertex in one request.
func (c *Client) VertexSubtaskPressure(jid, vid string, parallelism int) (SubtaskPressure, error) {
	sp := SubtaskPressure{
		Busy:          make([]float64, parallelism),
		Backpressured: make([]float64, parallelism),
		Idle:          make([]float64, parallelism),
		InPool:        make([]float64, parallelism),
		OutPool:       make([]float64, parallelism),
	}
	if parallelism < 1 {
		return sp, nil
	}
	metrics := map[string][]float64{
		"busyTimeMsPerSecond":          sp.Busy,
		"backPressuredTimeMsPerSecond": sp.Backpressured,
		"idleTimeMsPerSecond":          sp.Idle,
		"inPoolUsage":                  sp.InPool,
		"outPoolUsage":                 sp.OutPool,
	}
	keys := make([]string, 0, parallelism*len(metrics))
	for i := 0; i < parallelism; i++ {
		for name := range metrics {
			keys = append(keys, fmt.Sprintf("%d.%s", i, name))
		}
	}
	var ms []model.Metric
	if err := c.get("/jobs/"+jid+"/vertices/"+vid+"/metrics?get="+strings.Join(keys, ","), &ms); err != nil {
		return sp, err
	}
	for _, m := range ms {
		dot := strings.IndexByte(m.ID, '.')
		if dot < 0 {
			continue
		}
		idx, err := strconv.Atoi(m.ID[:dot])
		if err != nil || idx >= parallelism {
			continue
		}
		dst, ok := metrics[m.ID[dot+1:]]
		if !ok {
			continue
		}
		var val float64
		if _, err := fmt.Sscanf(m.Value, "%g", &val); err == nil {
			dst[idx] = val
		}
	}
	return sp, nil
}

// VertexWatermark returns the vertex's minimum currentInputWatermark across
// subtasks in epoch millis, or 0 when no watermark exists yet.
func (c *Client) VertexWatermark(jid, vid string) (int64, error) {
	var v []model.Metric
	err := c.get("/jobs/"+jid+"/vertices/"+vid+"/watermarks", &v)
	if err != nil {
		return 0, err
	}
	const noWatermark = -9223372036854775808 // Long.MIN_VALUE
	minWM := int64(0)
	for _, m := range v {
		var wm int64
		if _, err := fmt.Sscanf(m.Value, "%d", &wm); err != nil || wm == noWatermark {
			continue
		}
		if minWM == 0 || wm < minWM {
			minWM = wm
		}
	}
	return minWM, nil
}

// VertexWatermarkDetail gathers what the watermark debugger needs for one
// vertex: per-subtask input watermarks (from /watermarks), and for sources
// (which have no input watermark) their output watermark and idle time.
type VertexWatermarkDetail struct {
	Subtasks   []int64 // per-subtask input watermarks (noWatermark filtered to 0)
	SourceIdle int64   // sourceIdleTime ms; -1 if absent (not a source)
	OutputWM   int64   // source output watermark epoch ms; 0 if absent
}

func (c *Client) VertexWatermarkDetail(jid, vid string) (VertexWatermarkDetail, error) {
	const noWatermark = -9223372036854775808
	var out VertexWatermarkDetail
	out.SourceIdle = -1
	var v []model.Metric
	if err := c.get("/jobs/"+jid+"/vertices/"+vid+"/watermarks", &v); err != nil {
		return out, err
	}
	for _, m := range v {
		var wm int64
		if _, err := fmt.Sscanf(m.Value, "%d", &wm); err == nil && wm != noWatermark {
			out.Subtasks = append(out.Subtasks, wm)
		}
	}
	// source-only signals: scan the subtask metric catalog for the
	// operator-prefixed idle/output-watermark metrics.
	names, err := c.ListMetricNames("/jobs/" + jid + "/vertices/" + vid + "/subtasks/metrics")
	if err != nil {
		return out, nil
	}
	var want []string
	for _, n := range names {
		if strings.HasSuffix(n, ".sourceIdleTime") || strings.HasSuffix(n, ".currentOutputWatermark") {
			want = append(want, n)
		}
	}
	if len(want) == 0 {
		return out, nil
	}
	ms, err := c.MetricValues("/jobs/"+jid+"/vertices/"+vid+"/subtasks/metrics", want)
	if err != nil {
		return out, nil
	}
	for _, m := range ms {
		f, ok := m.Float()
		if !ok {
			continue
		}
		if strings.HasSuffix(m.ID, ".sourceIdleTime") {
			if out.SourceIdle < 0 || int64(f) > out.SourceIdle {
				out.SourceIdle = int64(f)
			}
		} else if strings.HasSuffix(m.ID, ".currentOutputWatermark") && int64(f) != noWatermark {
			out.OutputWM = int64(f)
		}
	}
	return out, nil
}

// ListMetricNames returns the metric name catalog at any metrics endpoint
// (calling without ?get= lists available IDs).
func (c *Client) ListMetricNames(path string) ([]string, error) {
	var ms []model.Metric
	if err := c.get(path, &ms); err != nil {
		return nil, err
	}
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out, nil
}

// MetricValues fetches values for the given metric keys at a metrics
// endpoint.
func (c *Client) MetricValues(path string, keys []string) ([]model.Metric, error) {
	var ms []model.Metric
	err := c.getMetrics(path, keys, &ms)
	return ms, err
}

// JobManagerMetrics is a compact JVM snapshot from GET /jobmanager/metrics.
// Missing keys are tolerated: heap falls back to 0 and CPU load to -1.
func (c *Client) JobManagerMetrics() (model.JMMetrics, error) {
	ms, err := c.MetricValues("/jobmanager/metrics", []string{
		"Status.JVM.Memory.Heap.Used",
		"Status.JVM.Memory.Heap.Max",
		"Status.JVM.CPU.Load",
	})
	out := model.JMMetrics{CPULoad: -1}
	if err != nil {
		return out, err
	}
	for _, m := range ms {
		v, ok := m.Float()
		if !ok {
			continue
		}
		switch m.ID {
		case "Status.JVM.Memory.Heap.Used":
			out.HeapUsed = int64(v)
		case "Status.JVM.Memory.Heap.Max":
			out.HeapMax = int64(v)
		case "Status.JVM.CPU.Load":
			out.CPULoad = v
		}
	}
	return out, nil
}

// JobManagerConfig is GET /jobmanager/config.
func (c *Client) JobManagerConfig() ([]model.ConfigEntry, error) {
	var v []model.ConfigEntry
	err := c.get("/jobmanager/config", &v)
	return v, err
}

// AdaptiveScheduler reports whether the cluster runs the adaptive scheduler
// (the FLIP-291 rescale precondition).
func (c *Client) AdaptiveScheduler() (bool, error) {
	cfg, err := c.JobManagerConfig()
	if err != nil {
		return false, err
	}
	for _, e := range cfg {
		if e.Key == "jobmanager.scheduler" && strings.EqualFold(e.Value, "adaptive") {
			return true, nil
		}
	}
	return false, nil
}

// DeploymentMode detects how the cluster is deployed from its JobManager
// config (execution.target). The config is near-static, so a successful
// detection is cached for the client's life; a failed read is not cached, so
// it retries. An absent execution.target yields ModeUnknown (not Confident),
// which keeps the conservative safety copy — we never assert a mode we can't
// prove.
func (c *Client) DeploymentMode() model.ModeInfo {
	c.modeMu.Lock()
	if c.mode != nil {
		mi := *c.mode
		c.modeMu.Unlock()
		return mi
	}
	c.modeMu.Unlock()

	cfg, err := c.JobManagerConfig()
	if err != nil {
		return model.ModeInfo{Mode: model.ModeUnknown} // uncached — retry next time
	}
	get := func(k string) string {
		for _, e := range cfg {
			if e.Key == k {
				return e.Value
			}
		}
		return ""
	}
	mi := model.ModeInfo{Mode: model.ModeUnknown, ClusterID: get("kubernetes.cluster-id")}
	switch target := strings.ToLower(get("execution.target")); {
	case target == "":
		// config didn't expose it — stay unknown/unconfident, fall to heuristics later
	case strings.HasSuffix(target, "application"), target == "embedded":
		mi.Mode, mi.Source, mi.Confident = model.ModeApplication, "flink-config", true
	case strings.HasSuffix(target, "session"), target == "remote":
		mi.Mode, mi.Source, mi.Confident = model.ModeSession, "flink-config", true
	case target == "local":
		mi.Mode, mi.Source, mi.Confident = model.ModeStandalone, "flink-config", true
	}
	c.modeMu.Lock()
	c.mode = &mi
	c.modeMu.Unlock()
	return mi
}

// SetVertexParallelism is PUT /jobs/:jid/resource-requirements (FLIP-291):
// declaratively rescale one vertex. The API requires the COMPLETE map of
// every vertex's requirements, so this reads the current requirements and
// modifies only the target. The job restarts from its latest checkpoint;
// requires the adaptive scheduler.
// ResourceTargets returns each vertex's target (upper-bound) parallelism from
// the adaptive scheduler's resource requirements. A rescale is pending wherever
// this differs from the vertex's currently-running parallelism — the scheduler
// can take up to ~a minute to apply one, especially a scale-down.
func (c *Client) ResourceTargets(jid string) (map[string]int, error) {
	var rr map[string]struct {
		Parallelism struct {
			UpperBound int `json:"upperBound"`
		} `json:"parallelism"`
	}
	if err := c.get("/jobs/"+jid+"/resource-requirements", &rr); err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rr))
	for vid, req := range rr {
		out[vid] = req.Parallelism.UpperBound
	}
	return out, nil
}

func (c *Client) SetVertexParallelism(jid, vid string, parallelism int) error {
	var current map[string]struct {
		Parallelism struct {
			LowerBound int `json:"lowerBound"`
			UpperBound int `json:"upperBound"`
		} `json:"parallelism"`
	}
	if err := c.get("/jobs/"+jid+"/resource-requirements", &current); err != nil {
		return fmt.Errorf("read current requirements: %w", err)
	}
	if _, ok := current[vid]; !ok {
		return fmt.Errorf("vertex %s not present in resource requirements", vid)
	}
	body := map[string]any{}
	for id, req := range current {
		upper := req.Parallelism.UpperBound
		lower := req.Parallelism.LowerBound
		if id == vid {
			upper = parallelism
			if lower > upper {
				lower = 1
			}
		}
		body[id] = map[string]any{"parallelism": map[string]int{"lowerBound": lower, "upperBound": upper}}
	}
	return c.send(http.MethodPut, "/jobs/"+jid+"/resource-requirements", body, nil)
}

// VertexBusy returns the vertex's average busyTimeMsPerSecond across
// subtasks, or -1 when the cluster doesn't report it (pre-1.13 Flink).
func (c *Client) VertexBusy(jid, vid string) (float64, error) {
	var v []model.AggregatedMetric
	err := c.getMetrics("/jobs/"+jid+"/vertices/"+vid+"/subtasks/metrics", []string{"busyTimeMsPerSecond"}, &v)
	if err != nil {
		return -1, err
	}
	for _, m := range v {
		if m.ID == "busyTimeMsPerSecond" {
			return m.Avg, nil
		}
	}
	return -1, nil
}

// Backpressure is GET /jobs/:jid/vertices/:vid/backpressure.
func (c *Client) Backpressure(jid, vid string) (model.Backpressure, error) {
	var v model.Backpressure
	err := c.get("/jobs/"+jid+"/vertices/"+vid+"/backpressure", &v)
	return v, err
}
