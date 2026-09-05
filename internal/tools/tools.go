// Package tools is f9s's single tool registry: every operational capability
// defined once, served identically to MCP clients and to the internal
// agentic-investigate loop. Mutating tools are flagged so both frontends
// can gate them.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
	Run         func(ctx context.Context, args map[string]string) (string, error)
}

// Registry holds the tool set for one cluster context.
type Registry struct {
	tools []Tool
	ctx   string
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
		audit.Append(audit.Record{Context: r.ctx, Action: name, Target: args["jid"], Params: args, Result: res, Source: source})
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

// New builds the registry for one cluster.
func New(cl *client.Client, contextName string) *Registry {

	record := func(jid, action, path string) {
		_ = ledger.Append(contextName, ledger.Entry{
			Time: time.Now(), JID: jid, Job: jid, Action: action, Path: path,
		})
	}
	return &Registry{ctx: contextName, tools: []Tool{
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
			Params:      []Param{jidParam},
			Mutating:    true,
			Run: func(ctx context.Context, args map[string]string) (string, error) {
				if err := cl.CancelJob(args["jid"]); err != nil {
					return "", err
				}
				return "cancel requested for " + args["jid"], nil
			},
		},
	}}
}
