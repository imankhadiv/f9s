package views

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/aymanbagabas/go-osc52/v2"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/ledger"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

// archEvent is one row of a job's life story.
type archEvent struct {
	at     time.Time
	source string // live / history / ledger
	event  string // RUNNING/FINISHED/... or savepoint/stop
	jid    string
	dur    int64
	path   string // savepoint path for ledger rows
}

type archaeologyMsg struct {
	events []archEvent
	err    error
}

// Archaeology fuses the ledger, the History Server, and the live cluster
// into "every run of this job, ever" — rollback-to-known-good becomes a
// lookup instead of a Slack-scrollback investigation.
type Archaeology struct {
	deps   Deps
	name   string
	events []archEvent
	tbl    *uitable.Model
	hsOff  bool
	err    string
}

// NewArchaeology builds the history view for one job name.
func NewArchaeology(deps Deps, name string) *Archaeology {
	tbl := newTable([]uitable.Column{
		{Title: "WHEN", Width: 17},
		{Title: "SOURCE", Width: 8},
		{Title: "EVENT", Width: 11},
		{Title: "JID", Width: 34},
		{Title: "DURATION", Width: 9, Right: true},
		{Title: "SAVEPOINT", Width: -1},
	})
	return &Archaeology{deps: deps, name: name, tbl: tbl}
}

func (a *Archaeology) Title() string   { return "archaeology" }
func (a *Archaeology) Filtering() bool { return false }
func (a *Archaeology) Bindings() []key.Binding {
	return []key.Binding{
		binding("y", "y", "copy savepoint/jid"),
		binding("esc", "esc", "back"),
	}
}

func (a *Archaeology) Init() tea.Cmd { return a.Refresh() }

func (a *Archaeology) Refresh() tea.Cmd {
	cl := a.deps.CL
	name := a.name
	ctx := a.deps.Ctx
	hsURL := a.deps.Cfg.Contexts[ctx].HistoryServer
	return func() tea.Msg {
		var evs []archEvent
		if jobs, err := cl.Jobs(); err == nil {
			for _, j := range jobs {
				if j.Name != name {
					continue
				}
				evs = append(evs, archEvent{
					at: time.UnixMilli(j.StartTime), source: "live", event: j.State,
					jid: j.JID, dur: j.Duration,
				})
			}
		}
		if hsURL != "" {
			hs := client.New(config.Context{URL: hsURL})
			if jobs, err := hs.Jobs(); err == nil {
				for _, j := range jobs {
					if j.Name != name {
						continue
					}
					evs = append(evs, archEvent{
						at: time.UnixMilli(j.StartTime), source: "history", event: j.State,
						jid: j.JID, dur: j.Duration,
					})
				}
			}
		}
		if entries, err := ledger.List(ctx); err == nil {
			for _, e := range entries {
				if e.Job != name {
					continue
				}
				evs = append(evs, archEvent{at: e.Time, source: "ledger", event: e.Action, jid: e.JID, path: e.Path})
			}
		}
		sort.SliceStable(evs, func(x, y int) bool { return evs[x].at.After(evs[y].at) })
		return archaeologyMsg{events: evs}
	}
}

func (a *Archaeology) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case archaeologyMsg:
		if msg.err != nil {
			a.err = msg.err.Error()
			return a, nil
		}
		a.err = ""
		a.events = msg.events
		a.hsOff = a.deps.Cfg.Contexts[a.deps.Ctx].HistoryServer == ""
		rows := make([][]uitable.Cell, 0, len(msg.events))
		keys := make([]string, 0, len(msg.events))
		for i, e := range msg.events {
			keys = append(keys, fmt.Sprintf("%s|%d", e.jid, i))
			srcStyle := styles.Dim()
			if e.source == "live" {
				srcStyle = styles.OKText()
			}
			evStyle := styles.StateStyle(e.event)
			if e.source == "ledger" {
				evStyle = styles.WarnText()
			}
			dur := "-"
			if e.dur > 0 {
				dur = model.FmtDuration(e.dur)
			}
			rows = append(rows, []uitable.Cell{
				uitable.Styled(e.at.Format("2006-01-02 15:04"), styles.Dim()),
				uitable.Styled(e.source, srcStyle),
				uitable.Styled(e.event, evStyle),
				uitable.Styled(e.jid, styles.Dim()),
				uitable.Plain(dur),
				uitable.Plain(e.path),
			})
		}
		a.tbl.SetRowsKeyed(rows, keys)
		return a, nil
	case tea.KeyMsg:
		if msg.String() == "y" && len(a.events) > 0 {
			e := a.events[a.tbl.Cursor()]
			text := e.path
			what := "savepoint path"
			if text == "" {
				text, what = e.jid, "jid"
			}
			_, _ = osc52.New(text).WriteTo(os.Stderr)
			return a, flash(what + " copied: " + text)
		}
		a.tbl.Update(msg)
	}
	return a, nil
}

func (a *Archaeology) View(width, height int) string {
	sub := "live · history server · savepoint ledger, fused"
	if a.hsOff {
		sub = "live + ledger only — add historyServer: to this context for completed runs"
	}
	return framed(width, height, "Archaeology", a.tbl.Len(), a.name+" · "+sub, func(iw, ih int) string {
		out := ""
		used := 0
		if a.err != "" {
			out += errorBanner(a.err)
			used++
		}
		if a.tbl.Len() == 0 {
			return out + emptyState(" no recorded runs for this job name")
		}
		a.tbl.SetSize(iw, ih-used)
		return out + a.tbl.View()
	})
}
