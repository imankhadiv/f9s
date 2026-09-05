package ui

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
}

// newProposalModal builds the confirm dialog for an agent-proposed action,
// showing the exact tool call.
func newProposalModal(msg views.ProposalMsg) *modal {
	return &modal{jid: fmt.Sprint(msg.Args), name: msg.Job, proposal: &msg}
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

func newModal(kind views.ActionKind, jid, name, defaultDir string) *modal {
	m := &modal{kind: kind, jid: jid, name: name}
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
		return fmt.Sprintf("The AI proposed this mutating action. Exact call:\n  %s %v\nIt was NOT executed. Confirm to run it now.", m.proposal.Tool, m.proposal.Args)
	}
	if m.rescale != nil {
		return fmt.Sprintf("Sets parallelism of %q from %d to the value below via\nFLIP-291 resource requirements. The job RESTARTS from its latest\ncheckpoint. Requires jobmanager.scheduler: adaptive.",
			m.rescale.Vertex, m.rescale.Current)
	}
	switch m.kind {
	case views.ActionSavepoint:
		return "Takes a savepoint; the job keeps running."
	case views.ActionStop:
		return "Takes a savepoint, then stops the job gracefully."
	default:
		return "Stops the job WITHOUT taking a savepoint. State not covered\nby an externalized checkpoint is lost."
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
		body += styles.Dim().Render(label) + "\n" +
			m.input.View() + "\n\n" +
			styles.Dim().Render("<enter> go · <esc> abort")
	} else {
		body += styles.Dim().Render("<y> yes · <esc> no")
	}
	box := styles.ModalBox().MaxWidth(width - 2).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
