package views

import (
	"strings"
	"testing"
)

func TestDescribeFrameTitle(t *testing.T) {
	d := NewDescribe(Deps{}, "MyJob", map[string]any{"state": "RUNNING", "n": 2})
	out := d.View(80, 20)
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "MYJOB") {
		t.Errorf("frame title should be the entity name; first line = %q", first)
	}
	if !strings.Contains(out, "state") || !strings.Contains(out, "RUNNING") {
		t.Error("describe body should contain the marshaled fields")
	}
}
