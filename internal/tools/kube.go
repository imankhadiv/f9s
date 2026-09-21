package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/imankhadiv/f9s/internal/kube"
)

var (
	nsParam   = Param{Name: "namespace", Description: "Kubernetes namespace of the operator resource", Required: true}
	nameParam = Param{Name: "name", Description: "name of the FlinkDeployment (or FlinkSessionJob)", Required: true}
	kindParam = Param{Name: "kind", Description: `"flinkdeployment" (default) or "flinksessionjob"`}
)

// crParams are the params every operator tool takes, plus extra.
func crParams(extra ...Param) []Param {
	return append([]Param{nsParam, nameParam, kindParam}, extra...)
}

// kubeTarget validates the operator-tool args.
func (r *Registry) kubeTarget(args map[string]string) (kind, ns, name string, err error) {
	if r.kube == "" {
		return "", "", "", fmt.Errorf("no kubeContext configured for context %q — operator tools are disabled", r.ctx)
	}
	kind = strings.ToLower(strings.TrimSpace(args["kind"]))
	if kind == "" {
		kind = kube.KindDeployment
	}
	if kind != kube.KindDeployment && kind != kube.KindSessionJob {
		return "", "", "", fmt.Errorf("kind must be flinkdeployment or flinksessionjob, got %q", args["kind"])
	}
	ns, name = strings.TrimSpace(args["namespace"]), strings.TrimSpace(args["name"])
	if ns == "" || name == "" {
		return "", "", "", fmt.Errorf("namespace and name are required")
	}
	return kind, ns, name, nil
}

// patchTool builds an operator tool from a patch builder that may consult the
// live resource (current nonce, current overrides).
func (r *Registry) patchTool(name, desc, risk string, extra []Param,
	build func(res kube.Resource, args map[string]string) (map[string]any, string, error)) Tool {
	return Tool{
		Name: name, Description: desc, Params: crParams(extra...), Mutating: true, Risk: risk, NeedsKube: true,
		Run: func(ctx context.Context, args map[string]string) (string, error) {
			kind, ns, nm, err := r.kubeTarget(args)
			if err != nil {
				return "", err
			}
			res, err := kube.Get(r.kube, kind, ns, nm)
			if err != nil {
				return "", err
			}
			patch, done, err := build(res, args)
			if err != nil {
				return "", err
			}
			if err := kube.Patch(r.kube, kind, ns, nm, patch); err != nil {
				return "", err
			}
			return fmt.Sprintf("%s/%s: %s — the operator reconciles it shortly (watch RECONCILE in the operator view)", ns, nm, done), nil
		},
	}
}

func upgradeNote(res kube.Resource) string {
	if res.Job != nil && res.Job.UpgradeMode == "stateless" {
		return " (upgradeMode stateless: state is DROPPED)"
	}
	return ""
}

// kubeTools are the Flink Kubernetes Operator mutations. All go through
// kube.Patch: refused while the operator is busy, server dry-run first.
func (r *Registry) kubeTools() []Tool {
	return []Tool{
		r.patchTool("k8s_suspend_job",
			"Suspend an operator-managed job (spec.job.state=suspended). With upgradeMode savepoint the operator takes a savepoint first; last-state keeps HA metadata; stateless drops state.",
			"Suspends the job: the operator stops it and tears down its TaskManagers.\nupgradeMode decides what survives (stateless: nothing).",
			nil, func(res kube.Resource, _ map[string]string) (map[string]any, string, error) {
				if res.Job == nil {
					return nil, "", fmt.Errorf("a session cluster has no job to suspend")
				}
				return kube.JobStatePatch("suspended"), "suspend requested" + upgradeNote(res), nil
			}),
		r.patchTool("k8s_resume_job",
			"Resume a suspended operator-managed job (spec.job.state=running).",
			"Resumes the job from the state its upgradeMode preserved.",
			nil, func(res kube.Resource, _ map[string]string) (map[string]any, string, error) {
				if res.Job == nil {
					return nil, "", fmt.Errorf("a session cluster has no job to resume")
				}
				return kube.JobStatePatch("running"), "resume requested", nil
			}),
		r.patchTool("k8s_set_parallelism",
			"Set the job's default parallelism in the CR (spec.job.parallelism). The operator performs an upgrade using the job's upgradeMode. For one vertex use set_parallelism_override.",
			"Changes spec.job.parallelism: the operator UPGRADES the job (stop and\nredeploy) using its upgradeMode — stateless loses all state.",
			[]Param{{Name: "parallelism", Description: "new job parallelism, a positive integer", Required: true}},
			func(res kube.Resource, args map[string]string) (map[string]any, string, error) {
				p, err := strconv.Atoi(strings.TrimSpace(args["parallelism"]))
				if err != nil || p < 1 {
					return nil, "", fmt.Errorf("parallelism must be a positive integer, got %q", args["parallelism"])
				}
				if res.Job == nil {
					return nil, "", fmt.Errorf("a session cluster has no job parallelism")
				}
				return kube.ParallelismPatch(p), fmt.Sprintf("parallelism %d → %d requested%s", res.Job.Parallelism, p, upgradeNote(res)), nil
			}),
		r.patchTool("k8s_trigger_savepoint",
			"Ask the operator to take a manual savepoint (bumps spec.job.savepointTriggerNonce). The job keeps running.",
			"The operator takes a savepoint; the job keeps running.",
			nil, func(res kube.Resource, _ map[string]string) (map[string]any, string, error) {
				if res.Job == nil {
					return nil, "", fmt.Errorf("a session cluster has no job to savepoint")
				}
				return kube.SavepointPatch(res.Job), "savepoint requested", nil
			}),
		r.patchTool("k8s_restart_job",
			"Restart an operator-managed job (bumps spec.restartNonce); the operator restarts it using its upgradeMode.",
			"The operator RESTARTS the job using its upgradeMode (stateless loses state).",
			nil, func(res kube.Resource, _ map[string]string) (map[string]any, string, error) {
				return kube.RestartPatch(), "restart requested" + upgradeNote(res), nil
			}),
		r.patchTool("k8s_redeploy_from_savepoint",
			"Redeploy the job restoring ONLY from the given savepoint (spec.job.initialSavepointPath + savepointRedeployNonce; Flink Kubernetes Operator 1.10+). Current state is discarded.",
			"FULL REDEPLOY from the given savepoint: the job's current state is\nDISCARDED and replaced by the savepoint's. Needs operator 1.10+\n(older operators reject it at the server dry run).",
			[]Param{{Name: "savepoint_path", Description: "savepoint path to restore from", Required: true}},
			func(res kube.Resource, args map[string]string) (map[string]any, string, error) {
				path := strings.TrimSpace(args["savepoint_path"])
				if path == "" {
					return nil, "", fmt.Errorf("savepoint_path is required")
				}
				if res.Job == nil {
					return nil, "", fmt.Errorf("a session cluster has no job to redeploy")
				}
				return kube.RedeployPatch(path), "redeploy from " + path + " requested", nil
			}),
		r.patchTool("set_parallelism_override",
			"Pin ONE vertex's parallelism durably via pipeline.jobvertex-parallelism-overrides in the CR (survives upgrades and takes precedence over the autoscaler). parallelism 0 removes the pin.",
			"Pins a vertex's parallelism in the CR (durable; overrides the autoscaler).\nThe operator applies it with an upgrade/rescale of the job.",
			[]Param{
				{Name: "vertex", Description: "the vertex id (32 hex chars)", Required: true},
				{Name: "parallelism", Description: "parallelism to pin; 0 removes the pin", Required: true},
			},
			func(res kube.Resource, args map[string]string) (map[string]any, string, error) {
				p, err := strconv.Atoi(strings.TrimSpace(args["parallelism"]))
				if err != nil || p < 0 {
					return nil, "", fmt.Errorf("parallelism must be a non-negative integer, got %q", args["parallelism"])
				}
				merged, err := kube.MergeOverride(res.FlinkConfig[kube.OverridesKey], args["vertex"], p)
				if err != nil {
					return nil, "", err
				}
				var v *string
				if merged != "" {
					v = &merged
				}
				done := fmt.Sprintf("vertex %s pinned to %d", args["vertex"], p)
				if p == 0 {
					done = "pin removed for vertex " + args["vertex"]
				}
				return kube.ConfigPatch(map[string]*string{kube.OverridesKey: v}), done, nil
			}),
		r.patchTool("set_autoscaler_enabled",
			"Turn the operator autoscaler on or off for this resource (job.autoscaler.enabled).",
			"Changes job.autoscaler.enabled in the CR. Enabling lets the operator\nrescale the job on its own from now on.",
			[]Param{{Name: "enabled", Description: `"true" or "false"`, Required: true}},
			func(_ kube.Resource, args map[string]string) (map[string]any, string, error) {
				v := strings.ToLower(strings.TrimSpace(args["enabled"]))
				if v != "true" && v != "false" {
					return nil, "", fmt.Errorf(`enabled must be "true" or "false", got %q`, args["enabled"])
				}
				// write the current key and drop the legacy one, so no stale
				// fallback value can contradict what was just set
				return kube.ConfigPatch(map[string]*string{
					"job.autoscaler.enabled":                     &v,
					"kubernetes.operator.job.autoscaler.enabled": nil,
				}), "autoscaler enabled=" + v, nil
			}),
		r.patchTool("set_target_utilization",
			"Set the autoscaler's target utilization (job.autoscaler.target.utilization), a fraction in (0, 1].",
			"Changes job.autoscaler.target.utilization: the autoscaler will size\nvertices for the new busy target on its next evaluation.",
			[]Param{{Name: "value", Description: "target utilization, e.g. 0.7", Required: true}},
			func(_ kube.Resource, args map[string]string) (map[string]any, string, error) {
				f, err := strconv.ParseFloat(strings.TrimSpace(args["value"]), 64)
				if err != nil || f <= 0 || f > 1 {
					return nil, "", fmt.Errorf("value must be a fraction in (0, 1], got %q", args["value"])
				}
				v := strconv.FormatFloat(f, 'f', -1, 64)
				return kube.ConfigPatch(map[string]*string{"job.autoscaler.target.utilization": &v}), "target utilization " + v, nil
			}),
	}
}
