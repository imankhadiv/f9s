package kube

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const richDeployment = `{"items": [{
  "metadata": {"namespace": "stream", "name": "orders", "generation": 7},
  "spec": {
    "flinkVersion": "v1_20", "mode": "native",
    "flinkConfiguration": {
      "taskmanager.numberOfTaskSlots": "2",
      "job.autoscaler": {"enabled": true, "target.utilization": 0.7},
      "execution.checkpointing.interval": "30s"
    },
    "job": {"jarURI": "local:///opt/flink/usrlib/orders.jar", "parallelism": 4,
            "upgradeMode": "savepoint", "state": "running"}
  },
  "status": {
    "lifecycleState": "DEPLOYED",
    "observedGeneration": 6,
    "error": "{\"type\":\"org.apache.flink.kubernetes.operator.exception.ReconciliationException\",\n\"message\":\"boom\"}",
    "reconciliationStatus": {"state": "UPGRADING", "reconciliationTimestamp": 1758000000000},
    "jobStatus": {"jobName": "orders", "jobId": "a1b2", "state": "RUNNING",
      "upgradeSavepointPath": "s3://sp/savepoint-9",
      "savepointInfo": {
        "lastSavepoint": {"location": "s3://sp/savepoint-9", "timeStamp": 1758000300000, "triggerType": "UPGRADE"},
        "savepointHistory": [
          {"location": "s3://sp/savepoint-8", "timeStamp": 1757000000000, "triggerType": "PERIODIC"},
          {"location": "s3://sp/savepoint-9", "timeStamp": 1758000300000, "triggerType": "UPGRADE"},
          {"location": "", "timeStamp": 1}
        ]}}
  }
}]}`

func TestParseRichDeployment(t *testing.T) {
	ds, err := Parse([]byte(richDeployment))
	if err != nil || len(ds) != 1 {
		t.Fatalf("parse: %v %v", ds, err)
	}
	d := ds[0]
	if d.Session() || d.Job.Parallelism != 4 || d.Job.UpgradeMode != "savepoint" {
		t.Fatalf("job spec = %+v", d.Job)
	}
	if d.JobID != "a1b2" || d.ReconcileState != "UPGRADING" || d.ReconciledAt.IsZero() {
		t.Fatalf("status = %+v", d)
	}
	if drift, known := d.Drift(); !drift || !known {
		t.Fatalf("generation 7 vs observed 6 must drift, got %v %v", drift, known)
	}
	want := map[string]string{
		"taskmanager.numberOfTaskSlots":     "2",
		"job.autoscaler.enabled":            "true",
		"job.autoscaler.target.utilization": "0.7",
		"execution.checkpointing.interval":  "30s",
	}
	if !reflect.DeepEqual(d.FlinkConfig, want) {
		t.Fatalf("flattened config = %v", d.FlinkConfig)
	}
	if !strings.Contains(d.ErrorFull, "\"message\":\"boom\"") || strings.Contains(d.Error, "\n") {
		t.Fatalf("error first line %q / full %q", d.Error, d.ErrorFull)
	}
	if d.LastSavepoint == nil || d.LastSavepoint.TriggerType != "UPGRADE" {
		t.Fatalf("last savepoint = %+v", d.LastSavepoint)
	}
	if len(d.SavepointHistory) != 2 || d.SavepointHistory[0].Location != "s3://sp/savepoint-9" {
		t.Fatalf("history must drop empty entries and sort newest first: %+v", d.SavepointHistory)
	}
}

func TestDriftUnknownWithoutObservedGeneration(t *testing.T) {
	ds, _ := Parse([]byte(fixture))
	if _, known := ds[0].Drift(); known {
		t.Fatal("no observedGeneration must mean drift unknown, not 'in sync'")
	}
	if !ds[0].Session() {
		t.Fatal("a CR without spec.job is a session cluster")
	}
}

func TestParseSessionJobs(t *testing.T) {
	js, err := ParseSessionJobs([]byte(`{"items": [{
	  "metadata": {"namespace": "stream", "name": "wordcount", "generation": 2},
	  "spec": {"deploymentName": "shared-session", "job": {"jarURI": "https://x/wc.jar", "parallelism": 2}},
	  "status": {"lifecycleState": "STABLE", "observedGeneration": 2,
	    "jobStatus": {"jobId": "ff00", "state": "RUNNING"},
	    "reconciliationStatus": {"state": "DEPLOYED"}}
	}]}`))
	if err != nil || len(js) != 1 {
		t.Fatalf("parse: %v %v", js, err)
	}
	j := js[0]
	if j.DeploymentName != "shared-session" || j.JobID != "ff00" || j.Job.Parallelism != 2 {
		t.Fatalf("session job = %+v", j)
	}
	if drift, known := j.Drift(); drift || !known {
		t.Fatalf("in-sync session job drift = %v %v", drift, known)
	}
}

func TestParseEventsHandlesBothTimestampShapesNewestFirst(t *testing.T) {
	es, err := ParseEvents([]byte(`{"items": [
	  {"type": "Normal", "reason": "JobStatusChanged", "message": "Job status changed from CREATED to RUNNING",
	   "count": 1, "lastTimestamp": "2026-09-20T10:00:00Z", "involvedObject": {"kind": "FlinkDeployment", "name": "orders"}},
	  {"type": "Normal", "reason": "ScalingReport", "message": "Scaling vertices: ...",
	   "eventTime": "2026-09-20T11:00:00.000000Z", "series": {"count": 4, "lastObservedTime": "2026-09-20T12:00:00Z"},
	   "involvedObject": {"kind": "FlinkDeployment", "name": "orders"}}
	]}`))
	if err != nil || len(es) != 2 {
		t.Fatalf("parse: %v %v", es, err)
	}
	if es[0].Reason != "ScalingReport" || es[0].Count != 4 || es[0].Last.Hour() != 12 {
		t.Fatalf("series event not folded / not newest first: %+v", es[0])
	}
	if es[1].Count != 1 || es[1].Object != "FlinkDeployment/orders" {
		t.Fatalf("classic event = %+v", es[1])
	}
}

func TestParsePods(t *testing.T) {
	ps, err := ParsePods([]byte(`{"items": [
	  {"metadata": {"name": "orders-taskmanager-1-1", "labels": {"component": "taskmanager"}},
	   "spec": {"nodeName": "n2"},
	   "status": {"phase": "Running", "containerStatuses": [
	     {"ready": false, "restartCount": 3, "state": {"waiting": {"reason": "CrashLoopBackOff"}}}]}},
	  {"metadata": {"name": "orders-5d9", "labels": {"component": "jobmanager"}},
	   "status": {"phase": "Running", "startTime": "2026-09-20T09:00:00Z", "containerStatuses": [
	     {"ready": true, "restartCount": 1, "state": {"running": {}}, "lastState": {"terminated": {"reason": "OOMKilled"}}}]}}
	]}`))
	if err != nil || len(ps) != 2 {
		t.Fatalf("parse: %v %v", ps, err)
	}
	if ps[0].Component != "jobmanager" || !ps[0].Ready || ps[0].Reason != "last: OOMKilled" || ps[0].Started.IsZero() {
		t.Fatalf("jobmanager pod = %+v", ps[0])
	}
	if ps[1].Ready || ps[1].Restarts != 3 || ps[1].Reason != "CrashLoopBackOff" {
		t.Fatalf("taskmanager pod = %+v", ps[1])
	}
}

func TestReadsBindExplicitContext(t *testing.T) {
	var got [][]string
	defer SetRunner(func(_ context.Context, args ...string) ([]byte, error) {
		got = append(got, args)
		return []byte(`{"items": []}`), nil
	})()
	if _, err := SessionJobs("prod"); err != nil {
		t.Fatal(err)
	}
	if _, err := Events("prod", "stream", "orders"); err != nil {
		t.Fatal(err)
	}
	if _, err := Pods("prod", "stream", "orders"); err != nil {
		t.Fatal(err)
	}
	for _, args := range got {
		if len(args) < 2 || args[0] != "--context" || args[1] != "prod" {
			t.Fatalf("kubectl call not bound to the context: %v", args)
		}
	}
	if !strings.Contains(strings.Join(got[1], " "), "--field-selector involvedObject.name=orders") {
		t.Fatalf("events not scoped to the resource: %v", got[1])
	}
	if !strings.Contains(strings.Join(got[2], " "), "-l app=orders") {
		t.Fatalf("pods not selected by the operator label: %v", got[2])
	}
	if _, err := Pods(" ", "stream", "orders"); err == nil {
		t.Fatal("empty context must be refused before kubectl runs")
	}
	if len(got) != 3 {
		t.Fatalf("empty-context call reached kubectl: %d calls", len(got))
	}
}

func TestMissingCRDErrorIsExplained(t *testing.T) {
	defer SetRunner(func(context.Context, ...string) ([]byte, error) {
		return nil, errors.New(`error: the server doesn't have a resource type "flinksessionjobs"`)
	})()
	_, err := SessionJobs("prod")
	if err == nil || !strings.Contains(err.Error(), "Flink Kubernetes Operator installed") {
		t.Fatalf("err = %v", err)
	}
}

func TestErrorLines(t *testing.T) {
	got := ErrorLines(`{"type":"org.apache.flink.kubernetes.operator.exception.ReconciliationException","message":"Could not upgrade",` +
		`"throwableList":[{"type":"java.util.concurrent.TimeoutException","message":"savepoint timed out"}]}`)
	want := []string{"ReconciliationException: Could not upgrade", "caused by TimeoutException: savepoint timed out"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ErrorLines = %q", got)
	}
	if got := ErrorLines("plain\ntext"); len(got) != 2 {
		t.Fatalf("plain error = %q", got)
	}
	if ErrorLines("  ") != nil {
		t.Fatal("empty error must be nil")
	}
}
