// Package doctor lints a cluster and its running jobs for the config smells
// that page people later — popeye's idea, pointed at Flink.
package doctor

import (
	"fmt"
	"strings"

	"github.com/imankhadiv/f9s/internal/client"
)

// Severity orders findings.
type Severity int

const (
	SevOK Severity = iota
	SevInfo
	SevWarn
	SevCritical
)

func (s Severity) String() string {
	switch s {
	case SevCritical:
		return "CRIT"
	case SevWarn:
		return "WARN"
	case SevInfo:
		return "INFO"
	}
	return "OK"
}

// Finding is one lint result.
type Finding struct {
	Sev    Severity
	Area   string // "cluster" or the job name
	Text   string
	Remedy string
}

// Run executes every rule against the live cluster.
func Run(cl *client.Client) ([]Finding, error) {
	var out []Finding

	cfg := map[string]string{}
	if entries, err := cl.JobManagerConfig(); err == nil {
		for _, e := range entries {
			cfg[e.Key] = e.Value
		}
	} else {
		return nil, fmt.Errorf("cluster config: %w", err)
	}

	// --- cluster rules ---
	if v := cfg["high-availability.type"]; v == "" || strings.EqualFold(v, "NONE") {
		if v2 := cfg["high-availability"]; v2 == "" || strings.EqualFold(v2, "NONE") {
			out = append(out, Finding{SevWarn, "cluster",
				"no high-availability configured — a JobManager crash loses all running jobs",
				"set high-availability.type: kubernetes|zookeeper with HA storage"})
		}
	}
	if cfg["state.savepoints.dir"] == "" && cfg["execution.checkpointing.savepoint-dir"] == "" {
		out = append(out, Finding{SevWarn, "cluster",
			"no default savepoint directory — every savepoint needs a hand-typed target",
			"set execution.checkpointing.savepoint-dir (or state.savepoints.dir)"})
	}
	if v := cfg["web.cancel.enable"]; strings.EqualFold(v, "true") {
		out = append(out, Finding{SevInfo, "cluster",
			"web.cancel.enable is on — anyone with Web UI access can kill jobs",
			"disable it and cancel through audited tooling instead"})
	}
	if strings.Contains(strings.ToLower(cfg["state.backend.type"]+cfg["state.backend"]), "rocksdb") &&
		!strings.EqualFold(cfg["state.backend.incremental"], "true") {
		out = append(out, Finding{SevWarn, "cluster",
			"RocksDB without incremental checkpoints — full-state uploads every interval",
			"set state.backend.incremental: true"})
	}
	if !strings.EqualFold(cfg["taskmanager.network.memory.buffer-debloat.enabled"], "true") {
		out = append(out, Finding{SevInfo, "cluster",
			"buffer debloating off — checkpoint alignment suffers more under backpressure",
			"consider taskmanager.network.memory.buffer-debloat.enabled: true"})
	}
	clusterDefaultRestart := cfg["restart-strategy.type"] != "" || cfg["restart-strategy"] != ""

	// --- per-job rules ---
	jobs, err := cl.Jobs()
	if err != nil {
		return out, fmt.Errorf("jobs: %w", err)
	}
	for _, j := range jobs {
		if j.State != "RUNNING" {
			continue
		}
		name := j.Name
		if len(name) > 32 {
			name = name[:31] + "…"
		}
		name += " (" + j.JID[:min(8, len(j.JID))] + ")"
		cc, err := cl.CheckpointConfig(j.JID)
		if err != nil || cc.Interval <= 0 {
			out = append(out, Finding{SevCritical, name,
				"checkpointing is DISABLED — a failure loses all state",
				"set execution.checkpointing.interval"})
		} else {
			if cp, err := cl.Checkpoints(j.JID); err == nil && cp.Counts.Failed > 0 && cp.Counts.Completed == 0 {
				out = append(out, Finding{SevCritical, name,
					fmt.Sprintf("every checkpoint has failed (%d attempts, 0 completed)", cp.Counts.Failed),
					"open the checkpoints view (c) and the straggler breakdown (enter)"})
			}
		}
		if jc, err := cl.JobConfig(j.JID); err == nil {
			if strings.Contains(strings.ToLower(jc.ExecutionConfig.RestartStrategy), "default") && !clusterDefaultRestart {
				out = append(out, Finding{SevWarn, name,
					"default restart strategy with none configured cluster-wide — the job dies on the first transient failure",
					"set restart-strategy.type: exponential-delay (job or cluster level)"})
			}
		}
	}

	if len(out) == 0 {
		out = append(out, Finding{SevOK, "cluster", "no findings — configuration looks healthy", ""})
	}
	return out, nil
}
