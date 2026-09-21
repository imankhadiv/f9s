package views

import (
	"strings"
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/kube"
	"github.com/imankhadiv/f9s/internal/metricts"
	"github.com/imankhadiv/f9s/internal/model"
)

const (
	vMap  = "6d2677a0ecc3fd8df0b72ec675edf8f4"
	vSink = "bc764cd8ddf7a0cff126f51c16239658"
)

func autoscaleDag(t *testing.T, busySink float64) *DagView {
	t.Helper()
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	ring := metricts.New(0)
	for i := 0; i < 5; i++ {
		ring.Add(metricts.Key{Scope: metricts.ScopeVertex, ID: metricts.VertexID("jid-1", vSink), Metric: metricts.Busy},
			metricts.Point{Time: now.Add(time.Duration(i-5) * 30 * time.Second), Value: busySink})
	}
	cfg := &config.Config{Contexts: map[string]config.Context{"prod": {KubeContext: "prod-us"}}}
	d := NewDagView(Deps{Ctx: "prod", Cfg: cfg, Metrics: ring}, "jid-1", "orders")
	d.now = func() time.Time { return now }
	d.loadKube = func(kc string) ([]kube.FlinkDeployment, error) {
		if kc != "prod-us" {
			t.Fatalf("kube lookup used context %q", kc)
		}
		return []kube.FlinkDeployment{
			{Namespace: "x", Name: "other", JobID: "someone-else"},
			{Namespace: "stream", Name: "orders", JobID: "jid-1",
				FlinkConfig: map[string]string{"job.autoscaler.enabled": "true"}},
		}, nil
	}
	d.loadEvents = func(_, ns, name string) ([]kube.Event, error) {
		if ns != "stream" || name != "orders" {
			t.Fatalf("events for %s/%s", ns, name)
		}
		return []kube.Event{
			{Reason: "JobStatusChanged", Message: "RUNNING", Last: now},
			{Reason: "ScalingReport", Last: now.Add(-2 * time.Minute),
				Message: "Scaling execution enabled, begin scaling vertices:{ Vertex ID " + vMap + " | Parallelism 2 -> 4 | Processing capacity 1.00 -> 2.00 | Target data rate 2.00}"},
		}, nil
	}
	d.plan = model.Plan{Nodes: []model.PlanNode{{ID: vMap}, {ID: vSink, Inputs: []model.PlanInput{{ID: vMap}}}}}
	d.job = model.Job{Vertices: []model.Vertex{{ID: vMap, Name: "Map", Parallelism: 2}, {ID: vSink, Name: "Sink", Parallelism: 8}}}
	cmd := d.autoCmd()
	if cmd == nil {
		t.Fatal("autoscale lookup did not run with a kubeContext configured")
	}
	d.Update(cmd())
	return d
}

func TestDagAutoscalerOverlay(t *testing.T) {
	d := autoscaleDag(t, 100) // sink ~10% busy → autoscaled job may scale it down

	if !d.auto.linked || d.auto.mode != "on" || d.auto.name != "orders" {
		t.Fatalf("job not linked to its CR: %+v", d.auto)
	}
	if to, src := d.recommendation(vMap, 2); to != 4 || src != "operator" {
		t.Fatalf("map recommendation = %d %s, want operator 4", to, src)
	}
	if to, src := d.recommendation(vSink, 8); src != "estimate" || to != 2 {
		t.Fatalf("sink recommendation = %d %s, want estimate 2", to, src)
	}
	box := d.box(d.plan.Nodes[0], boxInner, false)
	if !strings.Contains(box, "2 ⇒ 4 ●") {
		t.Fatalf("operator decision not in box:\n%s", box)
	}
	if legend := d.autoLegend(); !strings.Contains(legend, "autoscaler ● on · stream/orders") || !strings.Contains(legend, "last ScalingReport") {
		t.Fatalf("legend = %s", legend)
	}
	if ctx, _ := d.AIContext(); !strings.Contains(ctx, "operator autoscaler suggests parallelism 4") {
		t.Fatalf("AI context:\n%s", ctx)
	}
	if d.autoCmd() != nil {
		t.Fatal("kubectl lookup must be throttled")
	}
}

func TestDagEstimateWithoutAutoscalerOnlyFlagsUpscale(t *testing.T) {
	d := NewDagView(Deps{Metrics: metricts.New(0)}, "j", "job")
	now := time.Now()
	d.now = func() time.Time { return now }
	for i := 0; i < 4; i++ {
		d.deps.Metrics.Add(metricts.Key{Scope: metricts.ScopeVertex, ID: metricts.VertexID("j", "idle"), Metric: metricts.Busy},
			metricts.Point{Time: now.Add(time.Duration(i-4) * time.Second), Value: 50})
		d.deps.Metrics.Add(metricts.Key{Scope: metricts.ScopeVertex, ID: metricts.VertexID("j", "hot"), Metric: metricts.Busy},
			metricts.Point{Time: now.Add(time.Duration(i-4) * time.Second), Value: 1000})
	}
	if _, src := d.recommendation("idle", 8); src != "" {
		t.Fatal("an idle vertex on a non-autoscaled job is not worth flagging")
	}
	if to, src := d.recommendation("hot", 2); src != "estimate" || to != 3 {
		t.Fatalf("saturated vertex estimate = %d %s", to, src)
	}
	if d.autoCmd() != nil {
		t.Fatal("no kubeContext → no kubectl")
	}
}
