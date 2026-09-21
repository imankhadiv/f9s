// Package kube surfaces Flink Kubernetes Operator resources by shelling out
// to kubectl — the operator's native tool. No kube client dependency: f9s
// stays a small binary, and environments without kubectl simply don't get
// this view.
package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// JobSpec is spec.job of a FlinkDeployment / FlinkSessionJob.
type JobSpec struct {
	JarURI                string   `json:"jarURI"`
	EntryClass            string   `json:"entryClass"`
	Args                  []string `json:"args"`
	Parallelism           int      `json:"parallelism"`
	UpgradeMode           string   `json:"upgradeMode"` // stateless / savepoint / last-state
	State                 string   `json:"state"`       // running / suspended
	InitialSavepointPath  string   `json:"initialSavepointPath"`
	AllowNonRestoredState *bool    `json:"allowNonRestoredState"`
	SavepointTriggerNonce *int64   `json:"savepointTriggerNonce"`
}

// Savepoint is one entry of the operator's savepoint bookkeeping.
type Savepoint struct {
	Location    string
	Time        time.Time
	TriggerType string // MANUAL / PERIODIC / UPGRADE / UNKNOWN
	FormatType  string
}

// FlinkDeployment is the subset of the operator's CRD f9s displays.
type FlinkDeployment struct {
	Namespace    string
	Name         string
	FlinkVersion string
	Lifecycle    string // status.lifecycleState: STABLE, UPGRADING, FAILED, ...
	JMStatus     string // status.jobManagerDeploymentStatus: READY, MISSING, ...
	JobState     string // status.jobStatus.state: RUNNING, ...
	JobName      string
	JobID        string // status.jobStatus.jobId — joins the CR to the REST job
	Error        string // first line of status.error
	ErrorFull    string // the whole status.error (often a JSON blob)

	Mode        string // spec.mode: native / standalone
	Job         *JobSpec
	FlinkConfig map[string]string // spec.flinkConfiguration, flattened to dotted keys

	Generation         int64 // metadata.generation
	ResourceVersion    string
	ObservedGeneration int64 // status.observedGeneration (0 = operator doesn't report it)
	ReconcileState     string
	ReconciledAt       time.Time
	UpgradeSavepoint   string
	LastSavepoint      *Savepoint
	SavepointHistory   []Savepoint
}

// Session reports whether the CR is a session cluster (no spec.job).
func (d FlinkDeployment) Session() bool { return d.Job == nil }

// Drift reports whether the operator has not yet reconciled the latest spec
// edit (known=false when the operator doesn't publish observedGeneration).
func (d FlinkDeployment) Drift() (drifting, known bool) {
	return driftOf(d.Generation, d.ObservedGeneration)
}

func driftOf(gen, observed int64) (bool, bool) {
	if gen <= 0 || observed <= 0 {
		return false, false
	}
	return gen > observed, true
}

// FlinkSessionJob is a job submitted into a session FlinkDeployment.
type FlinkSessionJob struct {
	Namespace          string
	Name               string
	DeploymentName     string // spec.deploymentName — the parent session cluster
	Job                *JobSpec
	Lifecycle          string
	JobState           string
	JobName            string
	JobID              string
	Error              string
	ErrorFull          string
	Generation         int64
	ResourceVersion    string
	ObservedGeneration int64
	ReconcileState     string
	ReconciledAt       time.Time
}

// Drift mirrors FlinkDeployment.Drift.
func (s FlinkSessionJob) Drift() (drifting, known bool) {
	return driftOf(s.Generation, s.ObservedGeneration)
}

// Event is one Kubernetes event on an operator resource.
type Event struct {
	Type    string // Normal / Warning
	Reason  string // e.g. ScalingReport, SavepointError, JobStatusChanged
	Message string
	Count   int
	Last    time.Time
	Object  string // involvedObject kind/name
	// Namespace/Name of the involved object (the operator resource).
	Namespace, Name string
}

// Pod is one JobManager / TaskManager pod of a deployment.
type Pod struct {
	Name      string
	Component string // jobmanager / taskmanager (operator label)
	Phase     string
	Ready     bool
	Restarts  int
	Reason    string // waiting reason, or last termination reason (OOMKilled …)
	Node      string
	Started   time.Time
}

type meta struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	Generation      int64  `json:"generation"`
	ResourceVersion string `json:"resourceVersion"`
}

type reconcile struct {
	State                   string `json:"state"`
	ReconciliationTimestamp int64  `json:"reconciliationTimestamp"`
}

type rawSavepoint struct {
	Location    string `json:"location"`
	TimeStamp   int64  `json:"timeStamp"`
	TriggerType string `json:"triggerType"`
	FormatType  string `json:"formatType"`
}

func (r *rawSavepoint) convert() *Savepoint {
	if r == nil || r.Location == "" {
		return nil
	}
	sp := &Savepoint{Location: r.Location, TriggerType: r.TriggerType, FormatType: r.FormatType}
	if r.TimeStamp > 0 {
		sp.Time = time.UnixMilli(r.TimeStamp)
	}
	return sp
}

type jobStatus struct {
	JobName              string `json:"jobName"`
	JobID                string `json:"jobId"`
	State                string `json:"state"`
	UpgradeSavepointPath string `json:"upgradeSavepointPath"`
	SavepointInfo        struct {
		LastSavepoint    *rawSavepoint  `json:"lastSavepoint"`
		SavepointHistory []rawSavepoint `json:"savepointHistory"`
	} `json:"savepointInfo"`
}

type crdList struct {
	Items []struct {
		Metadata meta `json:"metadata"`
		Spec     struct {
			FlinkVersion       string          `json:"flinkVersion"`
			Mode               string          `json:"mode"`
			Job                *JobSpec        `json:"job"`
			FlinkConfiguration json.RawMessage `json:"flinkConfiguration"`
		} `json:"spec"`
		Status struct {
			LifecycleState            string    `json:"lifecycleState"`
			JobManagerDeploymentState string    `json:"jobManagerDeploymentStatus"`
			Error                     string    `json:"error"`
			ObservedGeneration        int64     `json:"observedGeneration"`
			JobStatus                 jobStatus `json:"jobStatus"`
			ReconciliationStatus      reconcile `json:"reconciliationStatus"`
		} `json:"status"`
	} `json:"items"`
}

func millis(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// Parse decodes `kubectl get flinkdeployments -o json` output.
func Parse(data []byte) ([]FlinkDeployment, error) {
	var list crdList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	out := make([]FlinkDeployment, 0, len(list.Items))
	for _, it := range list.Items {
		st := it.Status
		d := FlinkDeployment{
			Namespace:          it.Metadata.Namespace,
			Name:               it.Metadata.Name,
			FlinkVersion:       it.Spec.FlinkVersion,
			Lifecycle:          st.LifecycleState,
			JMStatus:           st.JobManagerDeploymentState,
			JobState:           st.JobStatus.State,
			JobName:            st.JobStatus.JobName,
			JobID:              st.JobStatus.JobID,
			Error:              firstLine(st.Error),
			ErrorFull:          st.Error,
			Mode:               it.Spec.Mode,
			Job:                it.Spec.Job,
			FlinkConfig:        FlattenConfig(it.Spec.FlinkConfiguration),
			Generation:         it.Metadata.Generation,
			ResourceVersion:    it.Metadata.ResourceVersion,
			ObservedGeneration: st.ObservedGeneration,
			ReconcileState:     st.ReconciliationStatus.State,
			ReconciledAt:       millis(st.ReconciliationStatus.ReconciliationTimestamp),
			UpgradeSavepoint:   st.JobStatus.UpgradeSavepointPath,
			LastSavepoint:      st.JobStatus.SavepointInfo.LastSavepoint.convert(),
		}
		for i := range st.JobStatus.SavepointInfo.SavepointHistory {
			if sp := st.JobStatus.SavepointInfo.SavepointHistory[i].convert(); sp != nil {
				d.SavepointHistory = append(d.SavepointHistory, *sp)
			}
		}
		// newest first, whatever order the operator stored them in
		sort.SliceStable(d.SavepointHistory, func(i, j int) bool {
			return d.SavepointHistory[i].Time.After(d.SavepointHistory[j].Time)
		})
		out = append(out, d)
	}
	return out, nil
}

// ParseSessionJobs decodes `kubectl get flinksessionjobs -o json` output.
func ParseSessionJobs(data []byte) ([]FlinkSessionJob, error) {
	var list struct {
		Items []struct {
			Metadata meta `json:"metadata"`
			Spec     struct {
				DeploymentName string   `json:"deploymentName"`
				Job            *JobSpec `json:"job"`
			} `json:"spec"`
			Status struct {
				LifecycleState       string    `json:"lifecycleState"`
				Error                string    `json:"error"`
				ObservedGeneration   int64     `json:"observedGeneration"`
				JobStatus            jobStatus `json:"jobStatus"`
				ReconciliationStatus reconcile `json:"reconciliationStatus"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	out := make([]FlinkSessionJob, 0, len(list.Items))
	for _, it := range list.Items {
		st := it.Status
		out = append(out, FlinkSessionJob{
			Namespace:          it.Metadata.Namespace,
			Name:               it.Metadata.Name,
			DeploymentName:     it.Spec.DeploymentName,
			Job:                it.Spec.Job,
			Lifecycle:          st.LifecycleState,
			JobState:           st.JobStatus.State,
			JobName:            st.JobStatus.JobName,
			JobID:              st.JobStatus.JobID,
			Error:              firstLine(st.Error),
			ErrorFull:          st.Error,
			Generation:         it.Metadata.Generation,
			ResourceVersion:    it.Metadata.ResourceVersion,
			ObservedGeneration: st.ObservedGeneration,
			ReconcileState:     st.ReconciliationStatus.State,
			ReconciledAt:       millis(st.ReconciliationStatus.ReconciliationTimestamp),
		})
	}
	return out, nil
}

// ParseEvents decodes `kubectl get events -o json`, newest first.
func ParseEvents(data []byte) ([]Event, error) {
	var list struct {
		Items []struct {
			Type           string `json:"type"`
			Reason         string `json:"reason"`
			Message        string `json:"message"`
			Count          int    `json:"count"`
			LastTimestamp  string `json:"lastTimestamp"`
			EventTime      string `json:"eventTime"`
			FirstTimestamp string `json:"firstTimestamp"`
			Series         *struct {
				Count            int    `json:"count"`
				LastObservedTime string `json:"lastObservedTime"`
			} `json:"series"`
			InvolvedObject struct {
				Kind      string `json:"kind"`
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"involvedObject"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(list.Items))
	for _, it := range list.Items {
		e := Event{Type: it.Type, Reason: it.Reason, Message: strings.TrimSpace(it.Message), Count: it.Count,
			Object:    it.InvolvedObject.Kind + "/" + it.InvolvedObject.Name,
			Namespace: it.InvolvedObject.Namespace, Name: it.InvolvedObject.Name}
		// events.k8s.io v1 moved the timestamps; take the first one present
		for _, ts := range []string{it.LastTimestamp, it.EventTime, it.FirstTimestamp} {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				e.Last = t
				break
			}
		}
		if it.Series != nil {
			if it.Series.Count > e.Count {
				e.Count = it.Series.Count
			}
			if t, err := time.Parse(time.RFC3339, it.Series.LastObservedTime); err == nil && t.After(e.Last) {
				e.Last = t
			}
		}
		if e.Count == 0 {
			e.Count = 1
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out, nil
}

// ParsePods decodes `kubectl get pods -o json`.
func ParsePods(data []byte) ([]Pod, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				StartTime         string `json:"startTime"`
				ContainerStatuses []struct {
					Ready        bool `json:"ready"`
					RestartCount int  `json:"restartCount"`
					State        struct {
						Waiting *struct {
							Reason string `json:"reason"`
						} `json:"waiting"`
						Terminated *struct {
							Reason string `json:"reason"`
						} `json:"terminated"`
					} `json:"state"`
					LastState struct {
						Terminated *struct {
							Reason string `json:"reason"`
						} `json:"terminated"`
					} `json:"lastState"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	out := make([]Pod, 0, len(list.Items))
	for _, it := range list.Items {
		p := Pod{Name: it.Metadata.Name, Component: it.Metadata.Labels["component"], Phase: it.Status.Phase,
			Node: it.Spec.NodeName, Ready: len(it.Status.ContainerStatuses) > 0}
		if t, err := time.Parse(time.RFC3339, it.Status.StartTime); err == nil {
			p.Started = t
		}
		for _, cs := range it.Status.ContainerStatuses {
			p.Ready = p.Ready && cs.Ready
			p.Restarts += cs.RestartCount
			switch {
			case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
				p.Reason = cs.State.Waiting.Reason
			case cs.State.Terminated != nil && cs.State.Terminated.Reason != "":
				p.Reason = cs.State.Terminated.Reason
			case p.Reason == "" && cs.LastState.Terminated != nil:
				p.Reason = "last: " + cs.LastState.Terminated.Reason
			}
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Component != out[j].Component {
			return out[i].Component < out[j].Component // jobmanager before taskmanager
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// FlattenConfig turns spec.flinkConfiguration into dotted string keys. The
// operator historically took a flat map of strings; newer versions also
// accept nested YAML objects — both land in the same shape here.
func FlattenConfig(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	flatten("", m, out)
	return out
}

func flatten(prefix string, m map[string]any, out map[string]string) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch t := v.(type) {
		case map[string]any:
			flatten(key, t, out)
		case string:
			out[key] = t
		case float64:
			out[key] = strconv.FormatFloat(t, 'f', -1, 64)
		case bool:
			out[key] = strconv.FormatBool(t)
		case nil:
			out[key] = ""
		default:
			b, _ := json.Marshal(t)
			out[key] = string(b)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Runner executes kubectl with args and returns stdout. Swappable in tests.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// run is the process-wide kubectl runner.
var run Runner = execKubectl

// SetRunner replaces the kubectl runner and returns a restore func (tests).
func SetRunner(r Runner) (restore func()) {
	prev := run
	run = r
	return func() { run = prev }
}

func execKubectl(ctx context.Context, args ...string) ([]byte, error) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return nil, fmt.Errorf("kubectl not found in PATH — install it to see operator resources")
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return stdout.Bytes(), nil
}

// get runs `kubectl --context C <args>` bound to an explicit context: f9s
// never falls back to kubectl's ambient context, which may point at another
// cluster entirely.
func get(kubeContext, resource string, args ...string) ([]byte, error) {
	kubeContext = strings.TrimSpace(kubeContext)
	if kubeContext == "" {
		return nil, fmt.Errorf("kubeContext is required; refusing to use kubectl's ambient context")
	}
	full := append([]string{"--context", kubeContext}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := run(ctx, full...)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "doesn't have a resource type") || strings.Contains(msg, "the server could not find") {
			return nil, fmt.Errorf("no %s on this cluster — is the Flink Kubernetes Operator installed?", resource)
		}
		if strings.HasPrefix(msg, "kubectl not found") {
			return nil, err
		}
		return nil, fmt.Errorf("kubectl: %s", firstLine(msg))
	}
	return out, nil
}

func deploymentArgs(kubeContext string) ([]string, error) {
	kubeContext = strings.TrimSpace(kubeContext)
	if kubeContext == "" {
		return nil, fmt.Errorf("kubeContext is required; refusing to use kubectl's ambient context")
	}
	return []string{"--context", kubeContext, "get", "flinkdeployments", "-A", "-o", "json"}, nil
}

// Deployments lists FlinkDeployments across all namespaces via kubectl,
// explicitly bound to kubeContext.
func Deployments(kubeContext string) ([]FlinkDeployment, error) {
	args, err := deploymentArgs(kubeContext)
	if err != nil {
		return nil, err
	}
	data, err := get(kubeContext, "FlinkDeployment CRDs", args[2:]...)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// SessionJobs lists FlinkSessionJobs across all namespaces. A cluster whose
// operator predates the CRD returns an error the caller may ignore.
func SessionJobs(kubeContext string) ([]FlinkSessionJob, error) {
	data, err := get(kubeContext, "FlinkSessionJob CRDs", "get", "flinksessionjobs", "-A", "-o", "json")
	if err != nil {
		return nil, err
	}
	return ParseSessionJobs(data)
}

// Events lists the events on one operator resource, newest first.
func Events(kubeContext, namespace, name string) ([]Event, error) {
	data, err := get(kubeContext, "events", "get", "events", "-n", namespace,
		"--field-selector", "involvedObject.name="+name, "-o", "json")
	if err != nil {
		return nil, err
	}
	return ParseEvents(data)
}

// Pods lists a deployment's JobManager/TaskManager pods (the operator labels
// them app=<deployment name>).
func Pods(kubeContext, namespace, name string) ([]Pod, error) {
	data, err := get(kubeContext, "pods", "get", "pods", "-n", namespace, "-l", "app="+name, "-o", "json")
	if err != nil {
		return nil, err
	}
	return ParsePods(data)
}

// YAML returns the resource as `kubectl get <kind> <name> -n <ns> -o yaml`.
func YAML(kubeContext, kind, namespace, name string) (string, error) {
	data, err := get(kubeContext, kind, "get", kind, name, "-n", namespace, "-o", "yaml")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ErrorLines turns the operator's status.error into readable lines. The
// operator stores a JSON blob ({type, message, throwableList[]}); anything
// else is returned as-is, line by line.
func ErrorLines(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var e struct {
		Type          string `json:"type"`
		Message       string `json:"message"`
		ThrowableList []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"throwableList"`
	}
	if json.Unmarshal([]byte(raw), &e) != nil || (e.Type == "" && e.Message == "") {
		return strings.Split(raw, "\n")
	}
	out := []string{shortType(e.Type) + ": " + strings.TrimSpace(e.Message)}
	for _, t := range e.ThrowableList {
		out = append(out, "caused by "+shortType(t.Type)+": "+strings.TrimSpace(t.Message))
	}
	return out
}

// shortType drops the package from a Java class name.
func shortType(t string) string {
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		return t[i+1:]
	}
	return t
}

// EventsByReason lists events with one reason across all namespaces (e.g.
// every autoscaler ScalingReport on the cluster), newest first, in a single
// kubectl call.
func EventsByReason(kubeContext, reason string) ([]Event, error) {
	data, err := get(kubeContext, "events", "get", "events", "-A", "--field-selector", "reason="+reason, "-o", "json")
	if err != nil {
		return nil, err
	}
	return ParseEvents(data)
}
