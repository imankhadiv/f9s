package views

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/ledger"
)

func ledgerView(readOnly bool) *Savepoints {
	cfg := &config.Config{Contexts: map[string]config.Context{"dev": {ReadOnly: readOnly}}}
	v := NewSavepoints(Deps{Ctx: "dev", Cfg: cfg})
	v.Update(ledgerMsg{entries: []ledger.Entry{
		{Time: time.Now(), Job: "orders", Action: "savepoint", Path: "s3://sp/orders-1"},
		{Time: time.Now(), Job: "billing", Action: "rescale", Path: "Map→4"},
	}})
	return v
}

func TestDisposeProposesTheSelectedSavepoint(t *testing.T) {
	v := ledgerView(false)
	_, cmd := v.Update(keyMsg("D"))
	pm, ok := cmd().(ProposalMsg)
	if !ok || pm.Tool != "dispose_savepoint" || pm.Args["savepoint_path"] != "s3://sp/orders-1" || pm.Source != "tui" {
		t.Fatalf("proposal = %#v", cmd())
	}
}

func TestDisposeRefusesNonSavepointRowsAndHiddenRows(t *testing.T) {
	v := ledgerView(false)
	v.Update(tea.KeyMsg{Type: tea.KeyDown})
	if _, cmd := v.Update(keyMsg("D")); cmd == nil {
		t.Fatal("expected a refusal flash")
	} else if fm, ok := cmd().(FlashMsg); !ok || !fm.IsErr {
		t.Fatalf("rescale row must not be disposable: %#v", cmd())
	}

	v = ledgerView(false)
	v.tbl.SetFilter("no-such-row")
	_, cmd := v.Update(keyMsg("D"))
	if fm, ok := cmd().(FlashMsg); !ok || !strings.Contains(fm.Text, "select a savepoint") {
		t.Fatalf("an empty filter must select nothing, got %#v", cmd())
	}
}

func TestDisposeBlockedInReadOnlyContext(t *testing.T) {
	v := ledgerView(true)
	_, cmd := v.Update(keyMsg("D"))
	if fm, ok := cmd().(FlashMsg); !ok || !strings.Contains(fm.Text, "[RO]") {
		t.Fatalf("read-only context must block dispose, got %#v", cmd())
	}
}
