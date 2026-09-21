package kube

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Operator resource kinds f9s patches.
const (
	KindDeployment = "flinkdeployment"
	KindSessionJob = "flinksessionjob"
)

// Resource is the live state of one operator resource, read right before a
// patch so the guard rails see the truth, not a list refreshed 10s ago.
type Resource struct {
	Kind           string
	Lifecycle      string
	ReconcileState string
	Drifting       bool
	Generation     int64
	Job            *JobSpec
	FlinkConfig    map[string]string
	LastSavepoint  string
}

// Get reads one FlinkDeployment or FlinkSessionJob.
func Get(kubeContext, kind, namespace, name string) (Resource, error) {
	if kind != KindDeployment && kind != KindSessionJob {
		return Resource{}, fmt.Errorf("unsupported kind %q", kind)
	}
	data, err := get(kubeContext, kind, "get", kind, name, "-n", namespace, "-o", "json")
	if err != nil {
		return Resource{}, err
	}
	wrapped := append(append([]byte(`{"items":[`), data...), []byte(`]}`)...)
	if kind == KindSessionJob {
		ss, err := ParseSessionJobs(wrapped)
		if err != nil || len(ss) != 1 {
			return Resource{}, fmt.Errorf("decode %s %s/%s: %v", kind, namespace, name, err)
		}
		s := ss[0]
		drift, _ := s.Drift()
		conf := map[string]string{}
		var raw struct {
			Spec struct {
				FlinkConfiguration json.RawMessage `json:"flinkConfiguration"`
			} `json:"spec"`
		}
		if json.Unmarshal(data, &raw) == nil {
			conf = FlattenConfig(raw.Spec.FlinkConfiguration)
		}
		return Resource{Kind: kind, Lifecycle: s.Lifecycle, ReconcileState: s.ReconcileState, Drifting: drift,
			Generation: s.Generation, Job: s.Job, FlinkConfig: conf}, nil
	}
	ds, err := Parse(wrapped)
	if err != nil || len(ds) != 1 {
		return Resource{}, fmt.Errorf("decode %s %s/%s: %v", kind, namespace, name, err)
	}
	d := ds[0]
	drift, _ := d.Drift()
	r := Resource{Kind: kind, Lifecycle: d.Lifecycle, ReconcileState: d.ReconcileState, Drifting: drift,
		Generation: d.Generation, Job: d.Job, FlinkConfig: d.FlinkConfig}
	if d.LastSavepoint != nil {
		r.LastSavepoint = d.LastSavepoint.Location
	}
	return r, nil
}

// Busy explains why the operator should not be handed another spec change
// right now ("" when it is safe to patch).
func (r Resource) Busy() string {
	switch {
	case r.ReconcileState == "UPGRADING" || r.Lifecycle == "UPGRADING":
		return "the operator is mid-upgrade (reconciliation state UPGRADING) — wait for it to finish"
	case r.ReconcileState == "ROLLING_BACK" || r.Lifecycle == "ROLLING_BACK":
		return "the operator is rolling back — wait for it to finish"
	case r.Drifting:
		return fmt.Sprintf("a previous spec edit (generation %d) is not reconciled yet — stacking another change on it hides which one failed", r.Generation)
	}
	return ""
}

// Patch applies a JSON merge patch to an operator resource. It refuses while
// the operator is busy, validates the patch with a server-side dry run (so
// schema errors — e.g. a field this operator version doesn't know — surface
// before anything changes), then applies it.
func Patch(kubeContext, kind, namespace, name string, patch map[string]any) error {
	res, err := Get(kubeContext, kind, namespace, name)
	if err != nil {
		return err
	}
	if why := res.Busy(); why != "" {
		return fmt.Errorf("refusing to patch %s/%s: %s", namespace, name, why)
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	args := []string{"patch", kind, name, "-n", namespace, "--type", "merge", "-p", string(body)}
	if _, err := get(kubeContext, kind, append(args, "--dry-run=server")...); err != nil {
		return fmt.Errorf("server dry run rejected the change: %w", err)
	}
	_, err = get(kubeContext, kind, args...)
	return err
}

// nonce returns a value different from the current one (the operator acts
// on any change of a nonce field).
func nonce(current *int64) int64 {
	n := time.Now().Unix()
	if current != nil && n <= *current {
		n = *current + 1
	}
	return n
}

// JobStatePatch suspends ("suspended") or resumes ("running") the job.
func JobStatePatch(state string) map[string]any {
	return map[string]any{"spec": map[string]any{"job": map[string]any{"state": state}}}
}

// ParallelismPatch sets spec.job.parallelism (the operator upgrades the job
// using its upgradeMode).
func ParallelismPatch(p int) map[string]any {
	return map[string]any{"spec": map[string]any{"job": map[string]any{"parallelism": p}}}
}

// SavepointPatch bumps spec.job.savepointTriggerNonce: the operator takes a
// manual savepoint.
func SavepointPatch(job *JobSpec) map[string]any {
	var cur *int64
	if job != nil {
		cur = job.SavepointTriggerNonce
	}
	return map[string]any{"spec": map[string]any{"job": map[string]any{"savepointTriggerNonce": nonce(cur)}}}
}

// RestartPatch bumps spec.restartNonce: the operator restarts the job with
// its upgradeMode (state is kept for savepoint/last-state).
func RestartPatch() map[string]any {
	return map[string]any{"spec": map[string]any{"restartNonce": nonce(nil)}}
}

// RedeployPatch sets spec.job.initialSavepointPath and bumps
// savepointRedeployNonce: a full redeploy restoring ONLY from that savepoint
// (operator 1.10+; older operators reject the field at the dry run).
func RedeployPatch(path string) map[string]any {
	return map[string]any{"spec": map[string]any{"job": map[string]any{
		"initialSavepointPath": path, "savepointRedeployNonce": nonce(nil)}}}
}

// ConfigPatch sets (or, with a nil value, removes) spec.flinkConfiguration
// keys.
func ConfigPatch(kv map[string]*string) map[string]any {
	conf := map[string]any{}
	for k, v := range kv {
		if v == nil {
			conf[k] = nil
		} else {
			conf[k] = *v
		}
	}
	return map[string]any{"spec": map[string]any{"flinkConfiguration": conf}}
}

// OverridesKey is Flink's per-vertex parallelism override option.
const OverridesKey = "pipeline.jobvertex-parallelism-overrides"

var vertexID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// MergeOverride updates a "vid:p,vid:p" override string: p <= 0 removes the
// vertex. The result is sorted so repeated edits give stable specs.
func MergeOverride(current, vid string, p int) (string, error) {
	vid = strings.ToLower(strings.TrimSpace(vid))
	if !vertexID.MatchString(vid) {
		return "", fmt.Errorf("%q is not a Flink vertex id (32 hex chars)", vid)
	}
	m := map[string]int{}
	for _, part := range strings.Split(current, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			m[strings.ToLower(strings.TrimSpace(k))] = n
		}
	}
	if p > 0 {
		m[vid] = p
	} else {
		delete(m, vid)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ":" + strconv.Itoa(m[k])
	}
	return strings.Join(parts, ","), nil
}
