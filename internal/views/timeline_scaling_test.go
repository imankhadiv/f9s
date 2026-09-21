package views

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/kube"
)

func TestTimelineMergesAutoscalerDecisionsAndFlapping(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := &config.Config{Contexts: map[string]config.Context{"prod": {KubeContext: "prod-us"}}}
	tl := NewTimeline(Deps{Ctx: "prod", Cfg: cfg})
	t0 := time.Now().Add(-40 * time.Minute)
	report := func(min, from, to int) kube.Event {
		return kube.Event{Reason: "ScalingReport", Namespace: "stream", Name: "orders", Last: t0.Add(time.Duration(min) * time.Minute),
			Message: "Scaling execution enabled, begin scaling vertices:{ Vertex ID 6d2677a0ecc3fd8df0b72ec675edf8f4 | Parallelism " +
				strconv.Itoa(from) + " -> " + strconv.Itoa(to) + " | Processing capacity 1.00 -> 2.00 | Target data rate 2.00}"}
	}
	tl.loadScaling = func(kc string) ([]kube.Event, error) {
		if kc != "prod-us" {
			t.Fatalf("kube context %q", kc)
		}
		return []kube.Event{report(20, 2, 4), report(10, 4, 2), report(0, 2, 4)}, nil
	}
	cmd := tl.Init()
	if cmd == nil {
		t.Fatal("timeline must fetch scaling events when a kubeContext is set")
	}
	tl.Update(cmd())
	body := tl.render(160)
	for _, want := range []string{"autoscaler · stream/orders · 6d2677a0 2→4 (applied)", "AUTOSCALER FLAPPING", "reversed direction 2×"} {
		if !strings.Contains(body, want) {
			t.Errorf("timeline missing %q:\n%s", want, body)
		}
	}
	if tl.Refresh() != nil {
		t.Fatal("kubectl must be throttled")
	}
	if NewTimeline(Deps{Ctx: "x"}).Init() != nil {
		t.Fatal("no kubeContext → no kubectl")
	}
}
