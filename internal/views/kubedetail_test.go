package views

import (
	"strings"
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/kube"
)

func richKubeEntry() kubeEntry {
	d := kube.FlinkDeployment{
		Namespace: "stream", Name: "orders", FlinkVersion: "v1_20", Mode: "native",
		Lifecycle: "DEPLOYED", JMStatus: "READY", JobState: "RUNNING", JobID: "a1b2",
		ErrorFull: `{"type":"org.x.ReconciliationException","message":"Could not upgrade","throwableList":[{"type":"java.util.concurrent.TimeoutException","message":"savepoint timed out"}]}`,
		Job:       &kube.JobSpec{JarURI: "local:///orders.jar", Parallelism: 4, UpgradeMode: "stateless", State: "running"},
		FlinkConfig: map[string]string{
			"job.autoscaler.enabled": "true", "job.autoscaler.scaling.enabled": "false",
			"job.autoscaler.target.utilization": "0.7",
		},
		Generation: 7, ObservedGeneration: 6, ReconcileState: "UPGRADING", ReconciledAt: time.Now().Add(-time.Minute),
		LastSavepoint: &kube.Savepoint{Location: "s3://sp/9", Time: time.Now(), TriggerType: "UPGRADE"},
	}
	return kubeEntry{deploy: &d}
}

func TestKubeDetailBody(t *testing.T) {
	v := NewKubeDetail(Deps{}, "prod", richKubeEntry())
	body := v.body()
	for _, want := range []string{
		"FlinkDeployment (application)",
		"⟳ spec generation 7 not yet reconciled (operator at 6)",
		"ReconciliationException: Could not upgrade",
		"caused by TimeoutException: savepoint timed out",
		"upgrades and restarts drop all state",
		"s3://sp/9",
		"◐ advisory",
		"target.utilization",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail missing %q:\n%s", want, body)
		}
	}
}

func TestBuildEntriesNestsSessionJobsUnderTheirCluster(t *testing.T) {
	ds := []kube.FlinkDeployment{
		{Namespace: "a", Name: "session-1"},
		{Namespace: "a", Name: "app", Job: &kube.JobSpec{}},
	}
	ss := []kube.FlinkSessionJob{
		{Namespace: "a", Name: "orphan", DeploymentName: "gone"},
		{Namespace: "a", Name: "wc", DeploymentName: "session-1"},
		{Namespace: "b", Name: "other-ns", DeploymentName: "session-1"}, // same name, other namespace
	}
	es := buildEntries(ds, ss)
	var got []string
	for _, e := range es {
		got = append(got, e.name())
	}
	if strings.Join(got, ",") != "session-1,wc,app,orphan,other-ns" {
		t.Fatalf("tree order = %v", got)
	}
	if es[1].parent == nil || es[1].parent.Name != "session-1" || es[3].parent != nil || es[4].parent != nil {
		t.Fatal("parents wrong: session jobs must match on namespace + deploymentName")
	}
}

func TestAutoscalerMode(t *testing.T) {
	cases := []struct {
		conf map[string]string
		want string
	}{
		{nil, "off"},
		{map[string]string{"job.autoscaler.enabled": "true"}, "on"},
		{map[string]string{"job.autoscaler.enabled": "true", "job.autoscaler.scaling.enabled": "false"}, "advisory"},
		{map[string]string{"kubernetes.operator.job.autoscaler.enabled": "true"}, "on"}, // pre-1.8 key prefix
	}
	for _, c := range cases {
		if got := AutoscalerMode(c.conf); got != c.want {
			t.Errorf("AutoscalerMode(%v) = %s, want %s", c.conf, got, c.want)
		}
	}
}

func TestKubeDeploysListShowsTreeAndOpensDetail(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]config.Context{"prod": {KubeContext: "prod-us"}}}
	v := NewKubeDeploys(Deps{Ctx: "prod", Cfg: cfg})
	v.load = func(string) ([]kube.FlinkDeployment, error) {
		return []kube.FlinkDeployment{{Namespace: "a", Name: "session-1", Generation: 3, ObservedGeneration: 2, ReconcileState: "DEPLOYED"}}, nil
	}
	v.loadSession = func(string) ([]kube.FlinkSessionJob, error) {
		return []kube.FlinkSessionJob{{Namespace: "a", Name: "wc", DeploymentName: "session-1"}}, nil
	}
	v.Update(v.Init()())
	body := v.View(160, 20)
	for _, want := range []string{"SESSION", "└ wc", "DEPLOYED ⟳"} {
		if !strings.Contains(body, want) {
			t.Errorf("list missing %q:\n%s", want, body)
		}
	}
	_, cmd := v.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter must push the detail view")
	}
	if pm, ok := cmd().(PushMsg); !ok || pm.M.Title() != "session-1" {
		t.Fatalf("enter pushed %#v", cmd())
	}
}

func TestKubeEventsAndPodsRender(t *testing.T) {
	ev := NewKubeEvents(Deps{}, "kc", "a", "orders")
	ev.Update(kubeEventsMsg{events: []kube.Event{{Type: "Warning", Reason: "SavepointError", Message: "timed\nout", Count: 2, Last: time.Now()}}})
	if b := ev.View(120, 10); !strings.Contains(b, "SavepointError") || !strings.Contains(b, "timed out") {
		t.Fatalf("events view:\n%s", b)
	}
	pv := NewKubePods(Deps{}, "kc", "a", "orders")
	pv.Update(kubePodsMsg{pods: []kube.Pod{{Name: "orders-tm-1", Component: "taskmanager", Phase: "Running", Restarts: 2, Reason: "last: OOMKilled"}}})
	if b := pv.View(140, 10); !strings.Contains(b, "OOMKilled") || !strings.Contains(b, "taskmanager") {
		t.Fatalf("pods view:\n%s", b)
	}
}

func TestKubeDetailActionsProposeOperatorTools(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]config.Context{"prod": {KubeContext: "k"}}}
	v := NewKubeDetail(Deps{Ctx: "prod", Cfg: cfg}, "k", richKubeEntry())
	cases := map[string]string{
		"s": "k8s_trigger_savepoint", "z": "k8s_suspend_job", "x": "k8s_restart_job",
		"P": "k8s_set_parallelism", "b": "k8s_redeploy_from_savepoint",
		"o": "set_autoscaler_enabled", "u": "set_target_utilization",
	}
	for k, tool := range cases {
		_, cmd := v.Update(keyMsg(k))
		if cmd == nil {
			t.Fatalf("%s: no command", k)
		}
		pm, ok := cmd().(ProposalMsg)
		if !ok || pm.Tool != tool || pm.Source != "tui" || pm.Args["namespace"] != "stream" || pm.Args["name"] != "orders" {
			t.Fatalf("%s: proposal = %#v", k, cmd())
		}
		switch k {
		case "b":
			if pm.Input == nil || pm.Input.Default != "" || !strings.Contains(pm.Input.Label, "s3://sp/9") {
				t.Fatalf("redeploy must NOT prefill (hint only): %+v", pm.Input)
			}
		case "o":
			if pm.Args["enabled"] != "false" {
				t.Fatalf("autoscaler is advisory (on) → toggle proposes off, got %v", pm.Args)
			}
		case "P":
			if pm.Input == nil || pm.Input.Default != "4" {
				t.Fatalf("parallelism input default = %+v", pm.Input)
			}
		}
	}
}

func TestKubeDetailActionsBlockedReadOnly(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]config.Context{"prod": {KubeContext: "k", ReadOnly: true}}}
	v := NewKubeDetail(Deps{Ctx: "prod", Cfg: cfg}, "k", richKubeEntry())
	_, cmd := v.Update(keyMsg("x"))
	if fm, ok := cmd().(FlashMsg); !ok || !strings.Contains(fm.Text, "[RO]") {
		t.Fatalf("read-only must block operator actions, got %#v", cmd())
	}
}

func TestSessionClusterHasNoJobActions(t *testing.T) {
	d := kube.FlinkDeployment{Namespace: "a", Name: "session"}
	v := NewKubeDetail(Deps{}, "k", kubeEntry{deploy: &d})
	for _, k := range []string{"s", "z", "x", "P", "b", "o", "u"} {
		if v.action(k) != nil {
			t.Fatalf("session cluster offered %q", k)
		}
	}
}

func TestAutoscalerModeKeyPrecedence(t *testing.T) {
	conf := map[string]string{
		"kubernetes.operator.job.autoscaler.enabled": "true",  // legacy says on
		"job.autoscaler.enabled":                     "false", // current key wins
	}
	if got := AutoscalerMode(conf); got != "off" {
		t.Fatalf("current key must win over the legacy fallback, got %s", got)
	}
}
