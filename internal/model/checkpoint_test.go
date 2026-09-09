package model

import (
	"encoding/json"
	"testing"
)

// The "latest.failed" block is the only reliable source of a checkpoint failure
// reason, so its JSON tags must decode exactly as Flink emits them.
func TestCheckpointLatestFailedDecode(t *testing.T) {
	const body = `{
		"counts": {"completed": 40, "failed": 3},
		"latest": {
			"failed": {
				"id": 1042,
				"failure_message": "Checkpoint expired before completing",
				"failure_timestamp": 1788000000000,
				"num_acknowledged_subtasks": 6,
				"num_subtasks": 12
			},
			"restored": {"id": 1039, "is_savepoint": true, "external_path": "s3://sp/1039"}
		}
	}`
	var cp Checkpoints
	if err := json.Unmarshal([]byte(body), &cp); err != nil {
		t.Fatal(err)
	}
	f := cp.Latest.Failed
	if f.ID != 1042 || f.FailureMessage != "Checkpoint expired before completing" {
		t.Errorf("failed decode: %+v", f)
	}
	if f.FailureTimestamp == 0 || f.NumAcknowledged != 6 || f.NumSubtasks != 12 {
		t.Errorf("failed ack/ts decode: %+v", f)
	}
	if !cp.Latest.Restored.IsSavepoint || cp.Latest.Restored.ExternalPath != "s3://sp/1039" {
		t.Errorf("restored decode: %+v", cp.Latest.Restored)
	}
}
