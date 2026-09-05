package kube

import "testing"

// fixture mirrors a real Flink Kubernetes Operator FlinkDeployment list.
const fixture = `{
  "apiVersion": "v1",
  "items": [
    {
      "metadata": {"namespace": "stream", "name": "orders-enrichment"},
      "spec": {"flinkVersion": "v1_20"},
      "status": {
        "lifecycleState": "STABLE",
        "jobManagerDeploymentStatus": "READY",
        "jobStatus": {"jobName": "orders-enrichment", "state": "RUNNING"}
      }
    },
    {
      "metadata": {"namespace": "stream", "name": "fraud-scoring"},
      "spec": {"flinkVersion": "v1_20"},
      "status": {
        "lifecycleState": "FAILED",
        "jobManagerDeploymentStatus": "MISSING",
        "error": "back-off 5m0s restarting failed container\nmore detail",
        "jobStatus": {"state": "RECONCILING"}
      }
    }
  ]
}`

func TestParse(t *testing.T) {
	ds, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 2 {
		t.Fatalf("want 2 deployments, got %d", len(ds))
	}
	if ds[0].Lifecycle != "STABLE" || ds[0].JobState != "RUNNING" || ds[0].Namespace != "stream" {
		t.Fatalf("unexpected first deployment: %+v", ds[0])
	}
	if ds[1].Error != "back-off 5m0s restarting failed container" {
		t.Fatalf("error should be first line only, got %q", ds[1].Error)
	}
}

func TestParseEmpty(t *testing.T) {
	ds, err := Parse([]byte(`{"items": []}`))
	if err != nil || len(ds) != 0 {
		t.Fatalf("empty list: %v %v", ds, err)
	}
}
