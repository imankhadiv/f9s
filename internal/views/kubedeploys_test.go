package views

import (
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/kube"
)

func TestKubeDeploysUsesContextMappedToSelectedF9sContext(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]config.Context{
		"prod": {KubeContext: "prod-us"},
	}}
	v := NewKubeDeploys(Deps{Ctx: "prod", Cfg: cfg})
	calledWith := ""
	v.load = func(kubeContext string) ([]kube.FlinkDeployment, error) {
		calledWith = kubeContext
		return nil, nil
	}

	msg := v.Init()()
	if got := msg.(kubeMsg).err; got != nil {
		t.Fatal(got)
	}
	if calledWith != "prod-us" {
		t.Fatalf("loader context = %q, want prod-us", calledWith)
	}
	if footer := v.View(100, 30); !strings.Contains(footer, "context prod-us") {
		t.Fatalf("view does not identify Kubernetes context:\n%s", footer)
	}
}

func TestKubeDeploysMissingMappingDoesNotRunKubectl(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]config.Context{
		"prod": {},
	}}
	v := NewKubeDeploys(Deps{Ctx: "prod", Cfg: cfg})
	called := false
	v.load = func(string) ([]kube.FlinkDeployment, error) {
		called = true
		return nil, nil
	}

	msg := v.Init()().(kubeMsg)
	if called {
		t.Fatal("loader ran without an explicit kubeContext mapping")
	}
	if msg.err == nil || !strings.Contains(msg.err.Error(), "kubeContext") || !strings.Contains(msg.err.Error(), "prod") {
		t.Fatalf("missing mapping error = %v", msg.err)
	}
}

// Init's error arrives a frame after the view first renders. Until then the
// view must not advertise a kubectl invocation it will never make — least of
// all one with an empty --context, which is what this view refuses to run.
func TestKubeDeploysNeverAdvertisesAnEmptyKubeContext(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]config.Context{"prod": {}}}
	v := NewKubeDeploys(Deps{Ctx: "prod", Cfg: cfg})
	v.load = func(string) ([]kube.FlinkDeployment, error) { return nil, nil }

	_ = v.Init() // returned command deliberately not applied
	body := v.View(100, 20)

	if strings.Contains(body, "--context  ") || strings.Contains(body, "--context \n") {
		t.Errorf("view advertised kubectl with an empty context:\n%s", body)
	}
	if !strings.Contains(body, "kubeContext") {
		t.Errorf("view does not say how to fix the missing mapping:\n%s", body)
	}
}
