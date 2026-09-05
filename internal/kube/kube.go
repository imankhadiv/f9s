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
	"strings"
	"time"
)

// FlinkDeployment is the subset of the operator's CRD status f9s displays.
type FlinkDeployment struct {
	Namespace    string
	Name         string
	FlinkVersion string
	Lifecycle    string // status.lifecycleState: STABLE, UPGRADING, FAILED, ...
	JMStatus     string // status.jobManagerDeploymentStatus: READY, MISSING, ...
	JobState     string // status.jobStatus.state: RUNNING, ...
	JobName      string
	Error        string
}

type crdList struct {
	Items []struct {
		Metadata struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			FlinkVersion string `json:"flinkVersion"`
		} `json:"spec"`
		Status struct {
			LifecycleState            string `json:"lifecycleState"`
			JobManagerDeploymentState string `json:"jobManagerDeploymentStatus"`
			Error                     string `json:"error"`
			JobStatus                 struct {
				JobName string `json:"jobName"`
				State   string `json:"state"`
			} `json:"jobStatus"`
		} `json:"status"`
	} `json:"items"`
}

// Parse decodes `kubectl get flinkdeployments -o json` output.
func Parse(data []byte) ([]FlinkDeployment, error) {
	var list crdList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	out := make([]FlinkDeployment, 0, len(list.Items))
	for _, it := range list.Items {
		out = append(out, FlinkDeployment{
			Namespace:    it.Metadata.Namespace,
			Name:         it.Metadata.Name,
			FlinkVersion: it.Spec.FlinkVersion,
			Lifecycle:    it.Status.LifecycleState,
			JMStatus:     it.Status.JobManagerDeploymentState,
			JobState:     it.Status.JobStatus.State,
			JobName:      it.Status.JobStatus.JobName,
			Error:        firstLine(it.Status.Error),
		})
	}
	return out, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Deployments lists FlinkDeployments across all namespaces via kubectl.
// kubeContext optionally selects a kubeconfig context.
func Deployments(kubeContext string) ([]FlinkDeployment, error) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return nil, fmt.Errorf("kubectl not found in PATH — install it to see FlinkDeployments")
	}
	args := []string{"get", "flinkdeployments", "-A", "-o", "json"}
	if kubeContext != "" {
		args = append([]string{"--context", kubeContext}, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(msg, "doesn't have a resource type") || strings.Contains(msg, "the server could not find") {
			return nil, fmt.Errorf("no FlinkDeployment CRDs on this cluster — is the Flink Kubernetes Operator installed?")
		}
		return nil, fmt.Errorf("kubectl: %s", firstLine(msg))
	}
	return Parse(stdout.Bytes())
}
