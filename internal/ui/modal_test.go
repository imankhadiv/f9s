package ui

import (
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/views"
)

// The proposal modal must surface the REAL target (the jid in the tool args),
// not the on-screen page label — an injected call can aim at a different job,
// and the human's informed tap is the whole defense.
func TestProposalModalShowsRealTargetAndMismatch(t *testing.T) {
	m := newProposalModal(views.ProposalMsg{
		Tool: "cancel_job",
		Args: map[string]string{"jid": "real-target-77"},
		Job:  "focused-job",
	}, model.ModeInfo{})
	if !strings.Contains(m.jid, "real-target-77") {
		t.Errorf("modal subtitle must show the real target jid, got %q", m.jid)
	}
	// jid differs from the focused page → an explicit warning must appear.
	if !strings.Contains(m.jid, "⚠") || !strings.Contains(m.jid, "not the focused") {
		t.Errorf("mismatched target must be flagged, got %q", m.jid)
	}
}

// No spurious mismatch warning when the proposal targets the focused job.
func TestProposalModalNoWarningWhenTargetsMatch(t *testing.T) {
	m := newProposalModal(views.ProposalMsg{
		Tool: "trigger_savepoint",
		Args: map[string]string{"jid": "job-1"},
		Job:  "job-1",
	}, model.ModeInfo{})
	if strings.Contains(m.jid, "⚠") {
		t.Errorf("no mismatch warning expected when target == focused, got %q", m.jid)
	}
}

// Each mutating tool gets its own consequence copy — the most dangerous action
// must not get the blandest warning.
func TestProposalModalPerActionConsequence(t *testing.T) {
	cases := map[string]string{
		"cancel_job":          "LOST",
		"stop_with_savepoint": "STOPS",
		"trigger_savepoint":   "keeps running",
		"rescale_vertex":      "RESTARTS",
	}
	for tool, want := range cases {
		m := newProposalModal(views.ProposalMsg{Tool: tool, Args: map[string]string{"jid": "j"}}, model.ModeInfo{})
		if got := m.consequence(); !strings.Contains(got, want) {
			t.Errorf("%s consequence should mention %q, got %q", tool, want, got)
		}
	}
}

// renderArgs must be deterministic and lead with the jid, so the operator sees
// the exact resolved call the same way every time.
func TestRenderArgsJidFirstDeterministic(t *testing.T) {
	args := map[string]string{"parallelism": "4", "vertex": "Calc[50]", "jid": "abc"}
	got := renderArgs(args)
	if !strings.HasPrefix(got, "jid=abc") {
		t.Errorf("jid must lead, got %q", got)
	}
	if got != renderArgs(args) {
		t.Error("renderArgs must be deterministic")
	}
	if !strings.Contains(got, "parallelism=4") || !strings.Contains(got, "vertex=Calc[50]") {
		t.Errorf("all args must render, got %q", got)
	}
}

// A job-ending action's consequence must reflect the deployment mode: on a
// session cluster the cluster survives; on an application cluster it's torn
// down; when the mode is unknown we stay conservative and assert neither.
func TestModalCancelCopyIsModeAware(t *testing.T) {
	fate := func(mode model.DeploymentMode) string {
		return newModal(views.ActionCancel, "j", "job", "", model.ModeInfo{Mode: mode, Confident: true}).consequence()
	}
	if got := fate(model.ModeSession); !strings.Contains(got, "session cluster keeps running") {
		t.Errorf("session cancel must reassure, got %q", got)
	}
	if got := fate(model.ModeApplication); !strings.Contains(got, "tears the cluster down") {
		t.Errorf("application cancel must warn of teardown, got %q", got)
	}
	got := fate(model.ModeUnknown)
	if strings.Contains(got, "keeps running") || strings.Contains(got, "tears the cluster down") {
		t.Errorf("unknown mode must stay conservative (no fate claim), got %q", got)
	}
}

// The same mode-awareness must hold on the AI-proposal path for a job-ending
// tool — an injected cancel on an application cluster still carries the warning.
func TestProposalCancelCopyIsModeAware(t *testing.T) {
	m := newProposalModal(views.ProposalMsg{Tool: "cancel_job", Args: map[string]string{"jid": "j"}}, model.ModeInfo{Mode: model.ModeApplication, Confident: true})
	if got := m.consequence(); !strings.Contains(got, "tears the cluster down") {
		t.Errorf("application-mode AI cancel must warn of teardown, got %q", got)
	}
}

// An operator-initiated tool gets the registry's risk copy and never the
// "AI-proposed" framing; an Input turns the dialog into a prompt.
func TestTUIProposalModalUsesRegistryRiskAndInput(t *testing.T) {
	m := newProposalModal(views.ProposalMsg{
		Tool:   "dispose_savepoint",
		Args:   map[string]string{"savepoint_path": "s3://sp/1"},
		Source: "tui",
		Input:  &views.ProposalInput{Arg: "x", Label: "why", Default: "d"},
	}, model.ModeInfo{})
	if m.verb() != "confirm: dispose_savepoint" {
		t.Errorf("verb = %q", m.verb())
	}
	c := m.consequence()
	if strings.Contains(c, "AI-proposed") || !strings.Contains(c, "PERMANENTLY DELETES") {
		t.Errorf("consequence = %q", c)
	}
	if !m.needsInput || m.input.Value() != "d" {
		t.Error("Input must prompt with its default")
	}
	if !strings.Contains(m.view(100, 30), "why") {
		t.Error("input label missing from the dialog")
	}
	if !strings.Contains(m.jid, "savepoint_path=s3://sp/1") {
		t.Errorf("dialog must show the exact args, got %q", m.jid)
	}
}
