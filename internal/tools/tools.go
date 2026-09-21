// Package tools is f9s's single tool registry: every operational capability
// defined once, served identically to MCP clients and to the internal
// agentic-investigate loop. Mutating tools are flagged so both frontends
// can gate them.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/audit"
	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/ledger"
)

// Param is one string parameter of a tool.
type Param struct {
	Name        string
	Description string
	Required    bool
}

// Tool is one capability: name, schema, and the code that runs it.
type Tool struct {
	Name        string
	Description string
	Params      []Param
	Mutating    bool
	// Risk is the consequence copy the confirm dialog shows before a
	// mutating tool runs — the human's informed tap is the whole defense.
	Risk string
	Run  func(ctx context.Context, args map[string]string) (string, error)
}

// Registry holds the tool set for one cluster context.
type Registry struct {
	tools []Tool
	ctx   string
	kube  string // kubeconfig context for operator tools ("" = not configured)
}

// Option configures a registry.
type Option func(*Registry)

// WithKube enables the Flink Kubernetes Operator tools against an explicit
// kubeconfig context (the f9s context's kubeContext).
func WithKube(kubeContext string) Option {
	return func(r *Registry) { r.kube = strings.TrimSpace(kubeContext) }
}

// TargetOf names what a tool call acts on, for the audit trail: the job id,
// the operator resource (namespace/name), or the savepoint path.
func TargetOf(args map[string]string) string {
	switch {
	case args["jid"] != "":
		return args["jid"]
	case args["name"] != "":
		return args["namespace"] + "/" + args["name"]
	case args["savepoint_path"] != "":
		return args["savepoint_path"]
	}
	return ""
}

// RiskOf returns a tool's confirm-dialog consequence copy ("" if unknown).
func RiskOf(name string) string {
	if t, ok := New(nil, "").Get(name); ok {
		return t.Risk
	}
	return ""
}

// RunAudited executes a tool; mutating runs land in the audit trail with
// the given source ("mcp", "ai-proposal") regardless of outcome.
func (r *Registry) RunAudited(ctx context.Context, name string, args map[string]string, source string) (string, error) {
	t, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	out, err := t.Run(ctx, args)
	if t.Mutating {
		res := "ok"
		if err != nil {
			res = err.Error()
		}
		audit.Append(audit.Record{Context: r.ctx, Action: name, Target: TargetOf(args), Params: args, Result: res, Source: source})
	}
	return out, err
}

// List returns the tools, excluding mutating ones unless asked.
func (r *Registry) List(includeMutating bool) []Tool {
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		if t.Mutating && !includeMutating {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Get finds a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	for _, t := range r.tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

func jsonText(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	return string(data), err
}

// waitForSavepoint polls a triggered operation to completion.
func waitForSavepoint(cl *client.Client, jid, reqID string) (string, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		st, err := cl.SavepointStatus(jid, reqID)
		if err != nil {
			return "", err
		}
		if st.Status.ID == "COMPLETED" {
			if st.Failed() {
				return "", fmt.Errorf("operation failed: %s", st.Operation.FailureCause.Class)
			}
			return st.Operation.Location, nil
		}
	}
	return "", fmt.Errorf("savepoint still in progress after 2m; check the cluster")
}

var jidParam = Param{Name: "jid", Description: "the Flink job id", Required: true}

// waitForDisposal polls a savepoint disposal to completion.
func waitForDisposal(cl *client.Client, reqID string) error {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		st, err := cl.DisposalStatus(reqID)
		if err != nil {
			return err
		}
		if st.Status.ID == "COMPLETED" {
			if c := st.Operation.FailureCause.Class; c != "" {
				return fmt.Errorf("disposal failed: %s", c)
			}
			return nil
		}
	}
	return fmt.Errorf("disposal still in progress after 2m; check the cluster")
}

// onlyRecoveryPoint returns the running job for which path is the newest
// completed savepoint while its checkpoints are not retained (deleted when
// the job stops) — deleting it would leave nothing to restore from.
func onlyRecoveryPoint(cl *client.Client, path string) string {
	jobs, err := cl.Jobs()
	if err != nil {
		return ""
	}
	for _, j := range jobs {
		if j.State != "RUNNING" && j.State != "RESTARTING" {
			continue
		}
		cp, err := cl.Checkpoints(j.JID)
		if err != nil {
			continue
		}
		for _, h := range cp.History { // newest first
			if h.IsSavepoint && h.Status == "COMPLETED" {
				if h.ExternalPath == path {
					if cfg, err := cl.CheckpointConfig(j.JID); err == nil {
						if _, warn := analyze.Retention(cfg); warn {
							return j.JID
						}
					}
				}
				break
			}
		}
	}
	return ""
}

// New builds the registry for one cluster.
func New(cl *client.Client, contextName string, opts ...Option) *Registry {

	record := func(jid, action, path string) {
		_ = ledger.Append(contextName, ledger.Entry{
			Time: time.Now(), JID: jid, Job: jid, Action: action, Path: path,
		})
	}
	reg := &Registry{ctx: contextName}
	for _, o := range opts {
		o(reg)
	}
	reg.tools = []Tool{
		{
			Name:        "list_jobs",
			Description: "List all Flink jobs on the cluster with state, uptime, and task counts.",
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				jobs, err := cl.Jobs()
				if err != nil {
					return "", err
				}
				return jsonText(jobs)
			},
		},
		{
			Name:        "get_job",
			Description: "Get one job's detail: state, vertices with parallelism/status/IO.",
			Params:      []Param{jidParam},
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				job, err := cl.Job(args["jid"])
				if err != nil {
					return "", err
				}
				return jsonText(job)
			},
		},
		{
			Name:        "get_checkpoints",
			Description: "Get a job's checkpoint counts and recent history (status, size, duration, failures).",
			Params:      []Param{jidParam},
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				cp, err := cl.Checkpoints(args["jid"])
				if err != nil {
					return "", err
				}
				return jsonText(cp)
			},
		},
		{
			Name:        "get_exceptions",
			Description: "Get a job's root exception and recent exception history.",
			Params:      []Param{jidParam},
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				ex, err := cl.Exceptions(args["jid"])
				if err != nil {
					return "", err
				}
				return jsonText(ex)
			},
		},
		{
			Name:        "get_taskmanagers",
			Description: "List the cluster's TaskManagers with slots, memory, and heartbeat.",
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				tms, err := cl.TaskManagers()
				if err != nil {
					return "", err
				}
				return jsonText(tms)
			},
		},
		{
			Name:        "get_diagnosis_evidence",
			Description: "Gather f9s's curated diagnosis evidence bundle for a job: state, restarts, checkpoint history, per-vertex backpressure/busy, the computed bottleneck vertex, advisor suggestions, and recent exceptions. Ideal input for reasoning about what is wrong.",
			Params:      []Param{jidParam},
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				return analyze.BuildEvidence(cl, args["jid"])
			},
		},
		{
			Name:        "get_signal_history",
			Description: "Time-aligned trend series for a job: the last 10 checkpoints (id, status, duration ms, size bytes, age) and the restart count — reason about trajectory (climbing? step change? accelerating?) instead of a single snapshot.",
			Params:      []Param{jidParam},
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				cp, err := cl.Checkpoints(args["jid"])
				if err != nil {
					return "", err
				}
				type ckpt struct {
					ID       int64  `json:"id"`
					Status   string `json:"status"`
					Duration int64  `json:"duration_ms"`
					Size     int64  `json:"size_bytes"`
					AgeSec   int64  `json:"age_sec"`
				}
				now := time.Now().UnixMilli()
				series := []ckpt{} // never null in JSON — empty means "no checkpoints yet"
				for i, h := range cp.History {
					if i >= 10 {
						break
					}
					series = append(series, ckpt{h.ID, h.Status, h.EndToEndDuration, h.StateSize, (now - h.TriggerTimestamp) / 1000})
				}
				restarts := ""
				if ms, err := cl.JobMetrics(args["jid"], "numRestarts"); err == nil {
					for _, m := range ms {
						if m.ID == "numRestarts" {
							restarts = m.Value
						}
					}
				}
				return jsonText(map[string]any{
					"checkpoints_newest_first": series,
					"num_restarts":             restarts,
					"note":                     "durations climbing across consecutive ids = growing state or backpressure; a step change = something deployed or an environmental shift",
				})
			},
		},
		{
			Name:        "list_savepoints",
			Description: "List the savepoints f9s has recorded for this context (the savepoint ledger), newest first.",
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				entries, err := ledger.List(contextName)
				if err != nil {
					return "", err
				}
				return jsonText(entries)
			},
		},
		{
			Name:        "trigger_savepoint",
			Description: "Trigger a savepoint for a job and wait for it to complete. Returns the savepoint path and records it in the f9s ledger. The job keeps running.",
			Risk:        "Takes a savepoint; the job keeps running.",
			Params: []Param{jidParam,
				{Name: "target_directory", Description: "savepoint target directory; empty uses the cluster default"}},
			Mutating: true,
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				id, err := cl.TriggerSavepoint(args["jid"], args["target_directory"])
				if err != nil {
					return "", err
				}
				loc, err := waitForSavepoint(cl, args["jid"], id)
				if err != nil {
					return "", err
				}
				record(args["jid"], "savepoint", loc)
				return "savepoint completed: " + loc, nil
			},
		},
		{
			Name:        "stop_with_savepoint",
			Description: "Gracefully stop a job with a final savepoint. Returns the savepoint path and records it in the f9s ledger. THIS STOPS THE JOB.",
			Risk:        "Takes a final savepoint, then STOPS the job.",
			Params: []Param{jidParam,
				{Name: "target_directory", Description: "savepoint target directory; empty uses the cluster default"}},
			Mutating: true,
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				id, err := cl.StopWithSavepoint(args["jid"], args["target_directory"], false)
				if err != nil {
					return "", err
				}
				loc, err := waitForSavepoint(cl, args["jid"], id)
				if err != nil {
					return "", err
				}
				record(args["jid"], "stop", loc)
				return "job stopped with savepoint: " + loc, nil
			},
		},
		{
			Name:        "cancel_job",
			Description: "Cancel a job WITHOUT taking a savepoint. State not covered by an externalized checkpoint is lost. Prefer stop_with_savepoint.",
			Risk:        "Stops the job WITHOUT a savepoint. State not covered by an\nexternalized checkpoint is LOST. Prefer stop_with_savepoint.",
			Params:      []Param{jidParam},
			Mutating:    true,
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				if err := cl.CancelJob(args["jid"]); err != nil {
					return "", err
				}
				return "cancel requested for " + args["jid"], nil
			},
		},
		{
			Name: "rescale_vertex",
			Description: "Rescale ONE vertex to a new parallelism via FLIP-291 resource requirements " +
				"(requires jobmanager.scheduler: adaptive). The job restarts in place from its latest " +
				"checkpoint and the scheduler applies the change within about a minute — no savepoint " +
				"needed. Identify the vertex by its name (as shown in the evidence bundle) or its vertex id.",
			Params: []Param{jidParam,
				{Name: "vertex", Description: "vertex name (from the evidence) or vertex id", Required: true},
				{Name: "parallelism", Description: "new target parallelism, a positive integer", Required: true}},
			Mutating: true,
			Risk:     "Rescales a vertex via FLIP-291. The job RESTARTS from its latest\ncheckpoint. Requires jobmanager.scheduler: adaptive.",
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				p, err := strconv.Atoi(strings.TrimSpace(args["parallelism"]))
				if err != nil || p < 1 {
					return "", fmt.Errorf("parallelism must be a positive integer, got %q", args["parallelism"])
				}
				if ok, aerr := cl.AdaptiveScheduler(); aerr == nil && !ok {
					return "", fmt.Errorf("cluster is not running the adaptive scheduler (set jobmanager.scheduler: adaptive) — in-place rescale is unavailable")
				}
				job, err := cl.Job(args["jid"])
				if err != nil {
					return "", err
				}
				vid, name := "", strings.TrimSpace(args["vertex"])
				for _, v := range job.Vertices {
					if v.ID == name || v.Name == name {
						vid, name = v.ID, v.Name
						break
					}
				}
				if vid == "" {
					return "", fmt.Errorf("no vertex named or id'd %q in job %s", args["vertex"], args["jid"])
				}
				if err := cl.SetVertexParallelism(args["jid"], vid, p); err != nil {
					return "", err
				}
				record(args["jid"], "rescale", fmt.Sprintf("%s→%d", name, p))
				return fmt.Sprintf("rescale requested: %s → %d (adaptive scheduler restarts the job from its latest checkpoint and converges within ~1 minute)", name, p), nil
			},
		},
		{
			Name: "restore_from_savepoint",
			Description: "Submit an uploaded jar (session cluster) as a NEW job restored from a savepoint. " +
				"Use a jar id from the cluster's jar list and a savepoint path from list_savepoints or the " +
				"job's checkpoint history. Returns the new job id.",
			Params: []Param{
				{Name: "jar_id", Description: "id of a jar already uploaded to the cluster", Required: true},
				{Name: "savepoint_path", Description: "savepoint (or retained checkpoint) path to restore from", Required: true},
				{Name: "allow_non_restored", Description: "\"true\" to skip state for operators missing from the new job (that state is DROPPED); default false"},
			},
			Mutating: true,
			Risk: "Submits a NEW job from the jar, restoring state from the savepoint.\n" +
				"The running job (if any) is untouched — two copies may now run.\n" +
				"allow_non_restored=true silently DROPS state the new job has no operator for.",
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				path := strings.TrimSpace(args["savepoint_path"])
				if path == "" {
					return "", fmt.Errorf("savepoint_path is required")
				}
				allow := strings.EqualFold(strings.TrimSpace(args["allow_non_restored"]), "true")
				jid, err := cl.RunJar(strings.TrimSpace(args["jar_id"]), client.RunJarOptions{SavepointPath: path, AllowNonRestoredState: allow})
				if err != nil {
					return "", err
				}
				record(jid, "restore", path)
				return "submitted job " + jid + " restored from " + path, nil
			},
		},
		{
			Name: "dispose_savepoint",
			Description: "Permanently delete a savepoint's files and metadata via the cluster " +
				"(POST /savepoint-disposal). Irreversible. Never dispose a job's only recovery point.",
			Params: []Param{
				{Name: "savepoint_path", Description: "the savepoint path to delete", Required: true},
			},
			Mutating: true,
			Risk: "PERMANENTLY DELETES the savepoint's files. This cannot be undone —\n" +
				"no job can ever be restored from it again.",
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				path := strings.TrimSpace(args["savepoint_path"])
				if path == "" {
					return "", fmt.Errorf("savepoint_path is required")
				}
				if jid := onlyRecoveryPoint(cl, path); jid != "" {
					return "", fmt.Errorf("refusing: %s is the newest savepoint of running job %s, which does not retain checkpoints — it may be the job's only recovery point", path, jid)
				}
				id, err := cl.DisposeSavepoint(path)
				if err != nil {
					return "", err
				}
				if err := waitForDisposal(cl, id); err != nil {
					return "", err
				}
				record("", "dispose", path)
				return "disposed " + path, nil
			},
		},
	}
	return reg
}
