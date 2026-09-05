package incident

import (
	"strings"
	"testing"
	"time"
)

const seed = `JOB "payments" state=RUNNING
COMPUTED BOTTLENECK: "enrich-map" (busy but not backpressured downstream of backpressure)
ROOT EXCEPTION:
java.lang.OutOfMemoryError: Java heap space`

func TestMemoryRoundTrip(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	fp := Extract(seed, "payments", "backpressure")
	if fp.Exception != "java.lang.OutOfMemoryError" || fp.Bottleneck != "enrich-map" {
		t.Fatalf("bad fingerprint: %+v", fp)
	}
	if got := Inject("c", seed, "payments", "backpressure"); got != seed {
		t.Fatal("empty store must not inject")
	}
	Append("c", Record{Time: time.Now().Add(-time.Hour), JID: "j1", FP: fp,
		Hypothesis: "TM heap too small for the enrichment cache"})
	out := Inject("c", seed, "payments", "backpressure")
	if !strings.Contains(out, "SIMILAR PAST INCIDENT") || !strings.Contains(out, "heap too small") {
		t.Fatalf("recall missing:\n%s", out[:300])
	}
	// different exception + job must not match
	other := Extract("ROOT EXCEPTION:\norg.apache.kafka.common.errors.TimeoutException", "other-job", "")
	if Recall("c", other) != nil {
		t.Fatal("unrelated incident must not recall")
	}
}

func TestHypothesisOf(t *testing.T) {
	if h := HypothesisOf("HYPOTHESIS: the sink is slow.\nEVIDENCE: x"); h != "the sink is slow." {
		t.Fatalf("got %q", h)
	}
	if h := HypothesisOf("\n\nplain first line"); h != "plain first line" {
		t.Fatalf("got %q", h)
	}
}
