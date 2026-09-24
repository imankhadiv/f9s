package views

import (
	"strings"
	"testing"
	"time"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/sentinel"
)

func TestJobsVerdictHealthy(t *testing.T) {
	jobs := []model.JobOverview{
		{JID: "a", Name: "orders", State: "RUNNING"},
		{JID: "b", Name: "billing", State: "RUNNING"},
	}
	extras := map[string]jobExtra{"a": {ckptState: "COMPLETED"}, "b": {ckptState: "COMPLETED"}}
	txt, level := jobsVerdict(jobs, extras, nil)
	if level != "ok" {
		t.Fatalf("level = %q, want ok: %s", level, txt)
	}
	for _, want := range []string{"HEALTHY", "2 running"} {
		if !strings.Contains(txt, want) {
			t.Errorf("verdict missing %q: %q", want, txt)
		}
	}
}

func TestJobsVerdictNamesTheJobsInTrouble(t *testing.T) {
	jobs := []model.JobOverview{
		{JID: "a", Name: "orders", State: "RUNNING"},
		{JID: "b", Name: "billing", State: "RESTARTING"},
		{JID: "c", Name: "sessionizer", State: "RUNNING"},
	}
	extras := map[string]jobExtra{"c": {ckptState: "FAILED"}}
	alerts := []sentinel.Alert{{Job: "orders", Kind: "backpressure", OpenedAt: time.Now().Add(-12 * time.Minute)}}

	txt, level := jobsVerdict(jobs, extras, alerts)
	if level != "crit" {
		t.Fatalf("level = %q, want crit: %s", level, txt)
	}
	for _, want := range []string{"NEEDS ATTENTION", "billing", "RESTARTING", "orders", "backpressure", "sessionizer", "checkpoint"} {
		if !strings.Contains(txt, want) {
			t.Errorf("verdict missing %q: %q", want, txt)
		}
	}
}

func TestJobsVerdictCapsTheNames(t *testing.T) {
	var jobs []model.JobOverview
	for i := 0; i < 9; i++ {
		jobs = append(jobs, model.JobOverview{JID: string(rune('a' + i)), Name: "job-" + string(rune('a'+i)), State: "FAILING"})
	}
	txt, _ := jobsVerdict(jobs, nil, nil)
	if !strings.Contains(txt, "+6 more") {
		t.Errorf("verdict does not cap the list: %q", txt)
	}
}

func TestJobsVerdictQuietWhenNothingRuns(t *testing.T) {
	txt, level := jobsVerdict(nil, nil, nil)
	if txt != "" || level != "" {
		t.Fatalf("an empty cluster has no verdict to give: %q %q", txt, level)
	}
	finished := []model.JobOverview{{JID: "a", Name: "old", State: "FINISHED"}}
	if txt, level := jobsVerdict(finished, nil, nil); level == "crit" {
		t.Fatalf("a finished job is not an incident: %q", txt)
	}
}

func TestJobsViewShowsTheVerdict(t *testing.T) {
	cfg := &config.Config{RefreshRate: 2, Contexts: map[string]config.Context{"dev": {}}}
	v := NewJobs(Deps{Ctx: "dev", Cfg: cfg, Sentinel: sentinel.New()})
	v.Update(jobsMsg{owner: v, gen: v.gen, jobs: []model.JobOverview{
		{JID: "a", Name: "orders", State: "RESTARTING"},
		{JID: "b", Name: "billing", State: "RUNNING"},
	}})
	body := v.View(140, 20)
	if !strings.Contains(body, "NEEDS ATTENTION") || !strings.Contains(body, "orders") {
		t.Fatalf("jobs list has no verdict:\n%s", body)
	}

	h := NewHistoryJobs(Deps{Ctx: "dev", Cfg: cfg, Sentinel: sentinel.New()})
	h.Update(jobsMsg{owner: h, gen: h.gen, jobs: []model.JobOverview{{JID: "a", Name: "old", State: "FAILED"}}})
	if strings.Contains(h.View(140, 20), "NEEDS ATTENTION") {
		t.Error("completed-job history must not claim a live incident")
	}
}

func TestJobsVerdictFlagsPartiallyRunningTasks(t *testing.T) {
	jobs := []model.JobOverview{
		{JID: "a", Name: "orders", State: "RUNNING", Tasks: model.TaskCounts{Total: 5, Running: 2}},
		{JID: "b", Name: "billing", State: "RUNNING", Tasks: model.TaskCounts{Total: 4, Running: 4}},
	}
	txt, level := jobsVerdict(jobs, nil, nil)
	if level != "crit" || !strings.Contains(txt, "2/5 tasks running") || !strings.Contains(txt, "orders") {
		t.Fatalf("verdict = %q (%s)", txt, level)
	}
	if strings.Contains(txt, "billing") {
		t.Errorf("a fully-running job is not in trouble: %q", txt)
	}
}
