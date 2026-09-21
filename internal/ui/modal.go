package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/tools"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/views"
)

// modal is the centered confirm/input dialog shown before any mutating
// action. Destructive intent deserves more visual weight than a footer line.
type modal struct {
	kind       views.ActionKind
	jid        string
	name       string
	input      textinput.Model
	needsInput bool
	rescale    *views.RescaleMsg  // non-nil for the rescale dialog
	proposal   *views.ProposalMsg // non-nil for an agent proposal
	mode       model.ModeInfo
}

// clusterFate is the mode-aware line appended to a job-ending action's
// consequence: on a session cluster the cluster survives; on an application
// cluster ending the job tears it down. It speaks ONLY when detection is
// Confident — a heuristic guess must never unlock a reassuring "cluster
// survives", so an unproven mode keeps the conservative base copy.
func (m *modal) clusterFate() string {
	if !m.mode.Confident {
		return ""
	}
	switch m.mode.Mode {
	case model.ModeSession, model.ModeStandalone:
		return "\n\nThe session cluster keeps running — other jobs are unaffected."
	case model.ModeApplication:
		return "\n\n⚠ APPLICATION cluster — ending this job tears the cluster down."
	default:
		return ""
	}
}

// newProposalModal builds the confirm dialog for an agent-proposed action.
// The real target is the jid in the tool args, not the on-screen page label —
// an injected call can aim at a different job, so we surface the args' jid as
// the subtitle and flag it when it disagrees with the focused page.
func newProposalModal(msg views.ProposalMsg, mi model.ModeInfo) *modal {
	sub := renderArgs(msg.Args)
	if jid := msg.Args["jid"]; jid != "" && msg.Job != "" && jid != msg.Job {
		sub = "⚠ targets " + jid + " (not the focused " + msg.Job + ")\n" + sub
	}
	m := &modal{jid: sub, name: msg.Job, proposal: &msg, mode: mi}
	if in := msg.Input; in != nil {
		m.needsInput = true
		m.input = textinput.New()
		m.input.Prompt = ""
		m.input.CharLimit = 300
		m.input.SetValue(in.Default)
		m.input.Focus()
	}
	return m
}

// renderArgs formats tool args deterministically (jid first, then sorted) so
// the operator always sees the exact, fully-resolved call — the prose is the
// attack surface; these concrete args are ground truth.
func renderArgs(args map[string]string) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		if k != "jid" && args[k] != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(args))
	if jid := args["jid"]; jid != "" {
		parts = append(parts, "jid="+jid)
	}
	for _, k := range keys {
		parts = append(parts, k+"="+args[k])
	}
	return strings.Join(parts, "  ")
}

// newRescaleModal builds the FLIP-291 rescale dialog, prefilled with the
// advisor's default of doubling the current parallelism.
func newRescaleModal(msg views.RescaleMsg) *modal {
	m := &modal{jid: msg.JID, name: msg.Job, needsInput: true, rescale: &msg}
	m.input = textinput.New()
	m.input.Prompt = ""
	m.input.CharLimit = 5
	m.input.SetValue(strconv.Itoa(msg.Current * 2))
	m.input.Focus()
	return m
}

func newModal(kind views.ActionKind, jid, name, defaultDir string, mi model.ModeInfo) *modal {
	m := &modal{kind: kind, jid: jid, name: name, mode: mi}
	if kind == views.ActionSavepoint || kind == views.ActionStop {
		m.needsInput = true
		m.input = textinput.New()
		m.input.Prompt = ""
		m.input.CharLimit = 200
		m.input.SetValue(defaultDir)
		m.input.Focus()
	}
	return m
}

func (m *modal) verb() string {
	if m.proposal != nil {
		if m.proposal.Source == "tui" {
			return "confirm: " + m.proposal.Tool
		}
		return "agent proposal: " + m.proposal.Tool
	}
	if m.rescale != nil {
		return "rescale"
	}
	switch m.kind {
	case views.ActionSavepoint:
		return "savepoint"
	case views.ActionStop:
		return "stop+savepoint"
	default:
		return "cancel"
	}
}

func (m *modal) consequence() string {
	if m.proposal != nil {
		// The AI proposal gets the SAME per-action danger copy a hand-driven
		// action shows — the most dangerous action must not get the blandest
		// warning, since the human's informed tap is the whole defense.
		var risk string
		switch m.proposal.Tool {
		case "cancel_job":
			risk = "Stops the job WITHOUT a savepoint. State not covered by an\nexternalized checkpoint is LOST. Prefer stop_with_savepoint." + m.clusterFate()
		case "stop_with_savepoint":
			risk = "Takes a final savepoint, then STOPS the job." + m.clusterFate()
		case "trigger_savepoint":
			risk = "Takes a savepoint; the job keeps running."
		case "rescale_vertex":
			risk = "Rescales a vertex via FLIP-291. The job RESTARTS from its latest\ncheckpoint. Requires jobmanager.scheduler: adaptive."
		default:
			if risk = tools.RiskOf(m.proposal.Tool); risk == "" {
				risk = "Mutating action proposed by the AI."
			}
		}
		if m.proposal.Source == "tui" {
			return "NOT yet executed.\n\n" + risk
		}
		return "AI-proposed — NOT yet executed.\n\n" + risk + "\n\nConfirm to run it now."
	}
	if m.rescale != nil {
		return fmt.Sprintf("Sets parallelism of %q from %d to the value below via\nFLIP-291 resource requirements. The job RESTARTS from its latest\ncheckpoint. Requires jobmanager.scheduler: adaptive.",
			m.rescale.Vertex, m.rescale.Current)
	}
	switch m.kind {
	case views.ActionSavepoint:
		return "Takes a savepoint; the job keeps running."
	case views.ActionStop:
		return "Takes a savepoint, then stops the job gracefully." + m.clusterFate()
	default:
		return "Stops the job WITHOUT taking a savepoint. State not covered\nby an externalized checkpoint is lost." + m.clusterFate()
	}
}

// update returns (closed, confirmed, cmd).
func (m *modal) update(msg tea.KeyMsg) (bool, bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return true, false, nil
	case "enter":
		if m.needsInput {
			return true, true, nil
		}
	case "y", "Y":
		if !m.needsInput {
			return true, true, nil
		}
	case "n", "N":
		if !m.needsInput {
			return true, false, nil
		}
	}
	if m.needsInput {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return false, false, cmd
	}
	return false, false, nil
}

// view renders the dialog centered in the given body area.
func (m *modal) view(width, height int) string {
	title := styles.ErrText().Bold(true).Render(m.verb()) + "  " + styles.Header().Render(m.name)
	body := title + "\n" +
		styles.Dim().Render(m.jid) + "\n\n" +
		m.consequence() + "\n\n"
	if m.needsInput {
		m.input.Width = max(40, min(70, width-20))
		label := "target dir (empty = cluster default)"
		if m.rescale != nil {
			label = "new parallelism"
		}
		if m.proposal != nil && m.proposal.Input != nil {
			label = m.proposal.Input.Label
		}
		body += styles.Dim().Render(label) + "\n" +
			m.input.View() + "\n\n" +
			styles.Dim().Render("<enter> go · <esc> abort")
	} else {
		body += styles.Dim().Render("<y> yes · <esc> no")
	}
	box := styles.ModalBox().MaxWidth(width - 2).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
