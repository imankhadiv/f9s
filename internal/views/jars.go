package views

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aymanbagabas/go-osc52/v2"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/ledger"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/uitable"
)

type jarsMsg struct {
	jars []client.Jar
	err  error
}

type jarActionMsg struct {
	note string
	err  error
}

type restoreRisk int

const (
	restoreSafe restoreRisk = iota
	restoreUnknown
	restoreUnsafe
)

type restoreCheck struct {
	risk    restoreRisk
	message string
}

type restorePreflightMsg struct {
	// owner is the Jars view that asked for this check. The verdict decides
	// whether to submit, so it must be applied by the view whose client and
	// context the operator was looking at — never by one that replaced it.
	owner     *Jars
	jarID     string
	savepoint string
	check     restoreCheck
}

type restoreApproval struct {
	jarID     string
	savepoint string
	risk      restoreRisk
}

// promptKind is what the inline textinput is currently collecting.
type promptKind int

const (
	promptNone promptKind = iota
	promptUpload
	promptRestore
)

// Jars is the session-job submitter: upload a jar, run it, or restore it
// from a ledger savepoint — the stop→fix→restore loop without leaving f9s.
type Jars struct {
	deps   Deps
	tbl    *uitable.Model
	jars   []client.Jar
	input  textinput.Model
	prompt promptKind
	filter viewFilter
	err    string
	// approval is a one-shot acknowledgement for one exact restore pair.
	approval *restoreApproval
}

// NewJars builds the jar submitter view.
func NewJars(deps Deps) *Jars {
	tbl := newTable([]uitable.Column{
		{Title: "NAME", Width: 34},
		{Title: "ID", Width: 40},
		{Title: "UPLOADED", Width: 17},
		{Title: "ENTRY", Width: -1},
	})
	in := textinput.New()
	in.CharLimit = 512
	return &Jars{deps: deps, tbl: tbl, input: in, filter: newViewFilter()}
}

func (j *Jars) Title() string   { return "jars" }
func (j *Jars) Filtering() bool { return j.prompt != promptNone || j.filter.Active() }
func (j *Jars) Bindings() []key.Binding {
	return []key.Binding{
		binding("u", "u", "upload jar"),
		binding("enter", "enter", "run"),
		binding("S", "S", "run from savepoint"),
		binding("ctrl+x", "ctrl-x", "delete jar"),
		binding("/", "/", "filter"),
		binding("y", "y", "copy jar id"),
		binding("esc", "esc", "back"),
	}
}

func (j *Jars) Init() tea.Cmd { return j.Refresh() }

func (j *Jars) Refresh() tea.Cmd {
	cl := j.deps.CL
	return func() tea.Msg {
		jars, err := cl.ListJars()
		return jarsMsg{jars: jars, err: err}
	}
}

func (j *Jars) selected() (client.Jar, bool) {
	if len(j.jars) == 0 {
		return client.Jar{}, false
	}
	return j.jars[j.tbl.Cursor()], true
}

// latestSavepoint prefills the restore prompt from the ledger.
func (j *Jars) latestSavepoint() string {
	entries, err := ledger.List(j.deps.Ctx)
	if err != nil || len(entries) == 0 {
		return ""
	}
	return entries[0].Path
}

func (j *Jars) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case jarsMsg:
		if msg.err != nil {
			j.err = msg.err.Error()
			return j, nil
		}
		j.err = ""
		j.jars = msg.jars
		rows := make([][]uitable.Cell, 0, len(msg.jars))
		keys := make([]string, 0, len(msg.jars))
		for _, jar := range msg.jars {
			keys = append(keys, jar.ID)
			entry := ""
			if len(jar.Entry) > 0 {
				entry = jar.Entry[0].Name
			}
			rows = append(rows, []uitable.Cell{
				uitable.Plain(jar.Name),
				uitable.Styled(jar.ID, styles.Dim()),
				uitable.Styled(time.UnixMilli(jar.Uploaded).Format("2006-01-02 15:04"), styles.Dim()),
				uitable.Styled(entry, styles.Dim()),
			})
		}
		j.tbl.SetRowsKeyed(rows, keys)
		return j, nil

	case jarActionMsg:
		if msg.err != nil {
			return j, flashErrV(msg.err.Error())
		}
		return j, tea.Batch(flash(msg.note), j.Refresh())

	case restorePreflightMsg:
		if msg.owner != j {
			return j, nil // preflight for a view replaced by navigation/context switch
		}
		if msg.check.risk == restoreSafe {
			j.approval = nil
			return j, j.run(msg.jarID, msg.savepoint, false)
		}
		if j.approval != nil &&
			j.approval.jarID == msg.jarID &&
			j.approval.savepoint == msg.savepoint &&
			j.approval.risk == msg.check.risk {
			allowDroppedState := msg.check.risk == restoreUnsafe
			j.approval = nil
			return j, j.run(msg.jarID, msg.savepoint, allowDroppedState)
		}
		j.approval = &restoreApproval{
			jarID: msg.jarID, savepoint: msg.savepoint, risk: msg.check.risk,
		}
		instruction := "press S and enter the same path again to attempt one strict restore"
		if msg.check.risk == restoreUnsafe {
			instruction = "press S and enter the same path again to allow dropping that state once"
		}
		return j, flashErrV(msg.check.message + " — " + instruction)

	case tea.KeyMsg:
		if j.prompt != promptNone {
			switch msg.String() {
			case "esc":
				j.prompt = promptNone
				j.input.Blur()
				return j, nil
			case "enter":
				value := j.input.Value()
				kind := j.prompt
				j.prompt = promptNone
				j.input.Blur()
				return j, j.submit(kind, value)
			}
			var cmd tea.Cmd
			j.input, cmd = j.input.Update(msg)
			return j, cmd
		}
		if handled, cmd := j.filter.Handle(msg, j.tbl); handled {
			return j, cmd
		}
		switch msg.String() {
		case "u":
			if g := readOnlyGuard(j.deps); g != nil {
				return j, g
			}
			j.prompt = promptUpload
			j.input.Placeholder = "/path/to/job.jar"
			j.input.SetValue("")
			j.input.Focus()
			return j, nil
		case "enter":
			if g := readOnlyGuard(j.deps); g != nil {
				return j, g
			}
			if jar, ok := j.selected(); ok {
				return j, j.run(jar.ID, "", false)
			}
		case "S":
			if g := readOnlyGuard(j.deps); g != nil {
				return j, g
			}
			if _, ok := j.selected(); ok {
				j.prompt = promptRestore
				j.input.Placeholder = "savepoint path (file:/... or s3://...)"
				j.input.SetValue(j.latestSavepoint())
				j.input.Focus()
				return j, nil
			}
		case "y":
			if jar, ok := j.selected(); ok {
				_, _ = osc52.New(jar.ID).WriteTo(os.Stderr)
				return j, flash("jar id copied: " + jar.ID)
			}
		case "ctrl+x":
			if g := readOnlyGuard(j.deps); g != nil {
				return j, g
			}
			if jar, ok := j.selected(); ok {
				cl := j.deps.CL
				return j, func() tea.Msg {
					if err := cl.DeleteJar(jar.ID); err != nil {
						return jarActionMsg{err: err}
					}
					return jarActionMsg{note: "deleted " + jar.Name}
				}
			}
		default:
			j.tbl.Update(msg)
		}
	}
	return j, nil
}

// submit dispatches a completed prompt.
func (j *Jars) submit(kind promptKind, value string) tea.Cmd {
	switch kind {
	case promptUpload:
		if value == "" {
			return nil
		}
		cl := j.deps.CL
		return func() tea.Msg {
			id, err := cl.UploadJar(value)
			if err != nil {
				return jarActionMsg{err: err}
			}
			return jarActionMsg{note: "uploaded " + id}
		}
	case promptRestore:
		if value == "" {
			return flashErrV("restore needs a savepoint path")
		}
		if jar, ok := j.selected(); ok {
			return j.preflightRestore(jar.ID, value)
		}
	}
	return nil
}

// preflightRestore compares the target jar's plan against the savepoint's
// ORIGINATING job's plan (recovered via the ledger, live cluster, or
// History Server). Mismatched operator sets mean silently dropped or empty
// state — the scariest 3am unknown, checked before submission.
func (j *Jars) preflightRestore(jarID, savepoint string) tea.Cmd {
	deps := j.deps
	return func() tea.Msg {
		return restorePreflightMsg{
			owner: j, jarID: jarID, savepoint: savepoint,
			check: restorePreflight(deps, jarID, savepoint),
		}
	}
}

func restorePreflight(deps Deps, jarID, savepoint string) restoreCheck {
	cl := deps.CL
	jarPlan, err := cl.JarPlan(jarID)
	if err != nil || len(jarPlan.Nodes) == 0 {
		return restoreCheck{risk: restoreUnknown, message: "restore preflight unavailable: target jar plan could not be read"}
	}
	// find the savepoint's originating job in the ledger
	originJID := ""
	if entries, lerr := ledger.List(deps.Ctx); lerr == nil {
		for _, e := range entries {
			if e.Path == savepoint {
				originJID = e.JID
				break
			}
		}
	}
	if originJID == "" {
		return restoreCheck{risk: restoreUnknown, message: "restore preflight unavailable: savepoint origin is not in this context's ledger"}
	}
	originPlan, err := cl.JobPlan(originJID)
	historyServer := ""
	if deps.Cfg != nil {
		historyServer = deps.Cfg.Contexts[deps.Ctx].HistoryServer
	}
	if err != nil && historyServer != "" {
		hs := client.New(config.Context{URL: historyServer})
		originPlan, err = hs.JobPlan(originJID)
	}
	if err != nil || len(originPlan.Nodes) == 0 {
		return restoreCheck{risk: restoreUnknown, message: "restore preflight unavailable: originating job plan is unreachable"}
	}
	jarIDs := map[string]string{}
	for _, n := range jarPlan.Nodes {
		jarIDs[n.ID] = n.Description
	}
	var missing []string
	for _, n := range originPlan.Nodes {
		if _, ok := jarIDs[n.ID]; !ok {
			d := n.Description
			if len(d) > 22 {
				d = d[:21] + "…"
			}
			missing = append(missing, d)
		}
	}
	if len(missing) > 0 {
		shown := missing
		extra := ""
		if len(shown) > 2 {
			extra = fmt.Sprintf(" (+%d more)", len(shown)-2)
			shown = shown[:2]
		}
		return restoreCheck{
			risk:    restoreUnsafe,
			message: fmt.Sprintf("⚠ RESTORE PREFLIGHT: %d savepoint operators missing from this jar (%s%s) — that state would be DROPPED", len(missing), strings.Join(shown, ", "), extra),
		}
	}
	return restoreCheck{risk: restoreSafe}
}

// run submits the jar, optionally from a savepoint.
func (j *Jars) run(jarID, savepoint string, allowNonRestoredState bool) tea.Cmd {
	cl := j.deps.CL
	return func() tea.Msg {
		jid, err := cl.RunJar(jarID, client.RunJarOptions{
			SavepointPath:         savepoint,
			AllowNonRestoredState: allowNonRestoredState,
		})
		if err != nil {
			return jarActionMsg{err: err}
		}
		note := "job submitted: " + jid
		if savepoint != "" {
			note = "restored from savepoint: " + jid
		}
		return jarActionMsg{note: note}
	}
}

func (j *Jars) View(width, height int) string {
	return framed(width, height, "Jars", j.tbl.Len(), j.deps.Ctx, func(iw, ih int) string {
		out := ""
		used := 0
		if j.err != "" {
			out += errorBanner(j.err)
			used++
		}
		if fl := j.filter.Line(); fl != "" {
			out += fl
			used++
		}
		if j.prompt != promptNone {
			label := " upload: "
			if j.prompt == promptRestore {
				label = " restore from: "
			}
			out += styles.OKText().Render(label) + j.input.View() + "\n"
			used++
		}
		if j.tbl.Len() == 0 && j.prompt == promptNone {
			return out + emptyState("No jars uploaded. <u> uploads a local jar; <enter> runs it;\n<S> restores it from the newest ledger savepoint — the full\nstop→fix→resume loop without leaving the terminal.")
		}
		j.tbl.SetSize(iw, ih-used)
		return out + j.tbl.View()
	})
}
