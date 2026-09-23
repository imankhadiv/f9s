package ui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/sentinel"
	"github.com/imankhadiv/f9s/internal/views"
)

func TestResolveCommand(t *testing.T) {
	cases := map[string]string{
		"jobs": "jobs", "j": "jobs", "tm": "tms", "taskmanagers": "tms",
		"sp": "savepoints", "led": "savepoints", "ctx": "contexts",
		"k8s": "k8s", "sql": "sql", "q": "quit", "quit": "quit",
		"s": "savepoints", "ja": "jars", "jm": "jmlog",
		"c": "", "co": "", "cont": "contexts", "conf": "config",
		// an abbreviated alias is still unambiguous when every name it
		// prefixes leads to the same destination
		"ta": "tms", "tas": "tms", "taskmanager": "tms",
		"dep": "k8s", "deployment": "k8s", "ku": "k8s", "kub": "k8s",
		"le": "savepoints", "ledge": "savepoints",
		"cal": "log", "cha": "ask", "cos": "waste",
		"cf": "config", "ct": "contexts", "pl": "pulse",
		// ...and genuinely ambiguous once two destinations are in reach
		"l": "", "d": "", "t": "", "a": "",
		"zzz": "", "": "",
	}
	for in, want := range cases {
		if got := resolveCommand(in); got != want {
			t.Errorf("resolveCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommandCandidatesNamesAmbiguousDestinations(t *testing.T) {
	want := []string{"cluster", "contexts", "log", "config", "ask", "waste"}
	if got := commandCandidates("c"); !reflect.DeepEqual(got, want) {
		t.Fatalf("commandCandidates(%q) = %v, want %v", "c", got, want)
	}
}

// Every destination is reachable by a prefix of one of its own names, and a
// prefix that reaches exactly one destination always navigates.
func TestEveryCommandIsReachable(t *testing.T) {
	for _, target := range commandTargets {
		canonical := target.names[0]
		reached := false
		for _, name := range target.names {
			for i := 1; i <= len(name); i++ {
				if resolveCommand(name[:i]) == canonical {
					reached = true
				}
			}
		}
		if !reached {
			t.Errorf("no prefix of %v reaches %q", target.names, canonical)
		}
	}
}

// An exact name or alias always wins, even when it also prefixes other
// destinations (":log" must not be ambiguous just because "ledger" exists).
func TestExactNamesBeatPrefixAmbiguity(t *testing.T) {
	for _, target := range commandTargets {
		for _, name := range target.names {
			if got := resolveCommand(name); got != target.names[0] {
				t.Errorf("resolveCommand(%q) = %q, want %q", name, got, target.names[0])
			}
		}
	}
}

func TestFlashGuidanceFitsTerminalWidth(t *testing.T) {
	long := "unknown command :zzz  (" + commandList() + ")"
	for _, width := range []int{20, 60, 80, 200} {
		a := &App{width: width}
		if got := lipgloss.Width(a.fitFlash(long)); got > width-1 {
			t.Errorf("width %d: flash rendered %d columns", width, got)
		}
	}
	// before the first WindowSizeMsg there is nothing on screen to overflow
	unsized := &App{}
	if got := unsized.fitFlash(long); got != long {
		t.Errorf("unsized app truncated the flash to %q", got)
	}
}

func TestCommandGuideFitsAvailableWidth(t *testing.T) {
	for _, width := range []int{1, 10, 40, 200} {
		if got := lipgloss.Width(commandGuide(width)); got > width {
			t.Errorf("commandGuide(%d) width = %d", width, got)
		}
	}
}

// An ambiguous prefix is no longer a dead end: the dropdown highlights a
// destination and enter runs that one — the operator can see which.
func TestAmbiguousPrefixRunsTheHighlightedMatch(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]config.Context{
		"test": {URL: "http://unused"},
	}}
	cl := client.New(cfg.Contexts["test"])
	deps := views.Deps{CL: cl, Ctx: "test", Cfg: cfg}
	a := &App{
		cfg:         cfg,
		cl:          cl,
		contextName: "test",
		stack:       []views.Model{views.NewJobs(deps)},
		cmdMode:     true,
		cmdInput:    newCmdInput(),
	}
	a.cmdInput.SetValue("c") // cluster, contexts, log, config, ask, waste

	_, _ = a.Update(tea.KeyMsg{Type: tea.KeyDown}) // highlight "contexts"
	_, _ = a.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if a.flash != "" {
		t.Fatalf("flash = %q, want none", a.flash)
	}
	if got := a.top().Title(); got != "contexts" {
		t.Fatalf("ran %q, want the highlighted contexts", got)
	}
	if a.cmdMode {
		t.Fatal("command mode stayed open")
	}
}

// An input nothing matches still refuses, and says what exists.
func TestUnknownCommandFlashes(t *testing.T) {
	cfg := &config.Config{Contexts: map[string]config.Context{"test": {URL: "http://unused"}}}
	cl := client.New(cfg.Contexts["test"])
	deps := views.Deps{CL: cl, Ctx: "test", Cfg: cfg}
	a := &App{cfg: cfg, cl: cl, contextName: "test", width: 200,
		stack: []views.Model{views.NewJobs(deps)}, cmdMode: true, cmdInput: newCmdInput()}
	a.cmdInput.SetValue("zzz")

	_, _ = a.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if len(a.stack) != 1 {
		t.Fatalf("unknown command changed stack length to %d", len(a.stack))
	}
	if !strings.Contains(a.flash, "unknown command :zzz") {
		t.Fatalf("flash = %q", a.flash)
	}
}

// cmdLine renders the app in command mode and returns the last non-blank
// line — header + input + candidate hint + guide.
func cmdLine(t *testing.T, width int, input string) string {
	t.Helper()
	a := &App{
		width: width, height: 40, cmdMode: true,
		cmdInput:    newCmdInput(),
		stack:       []views.Model{&helpTestView{}},
		cfg:         &config.Config{Contexts: map[string]config.Context{"test": {URL: "http://unused"}}},
		contextName: "test",
		everSeen:    true,
		sent:        sentinel.NewPersistent("test"),
	}
	a.cmdInput.SetValue(input)
	last := ""
	for _, l := range strings.Split(a.View(), "\n") {
		if strings.TrimSpace(l) != "" {
			last = l
		}
	}
	return last
}

// No input may push the command line past the terminal. 60 columns is the
// narrowest terminal f9s renders at all, so it is the binding case; today the
// widest hint (":c", six destinations) lands at 55, and this keeps a newly
// added command from quietly spending that headroom.
func TestCommandLineFitsNarrowTerminal(t *testing.T) {
	seen := map[string]bool{}
	for _, target := range commandTargets {
		for _, name := range target.names {
			for i := 1; i <= len(name); i++ {
				seen[name[:i]] = true
			}
		}
	}
	for input := range seen {
		for _, width := range []int{60, 80, 120} {
			if got := lipgloss.Width(cmdLine(t, width, input)); got > width {
				t.Errorf("width %d, input %q: command line rendered %d columns", width, input, got)
			}
		}
	}
}

// The hint is fitted before styling, so it stays bounded even when a
// destination list outgrows the terminal on its own.
func TestWideCandidateHintIsTruncated(t *testing.T) {
	restore := commandTargets
	t.Cleanup(func() { commandTargets = restore })
	commandTargets = append(commandTargets[:len(commandTargets):len(commandTargets)],
		commandTarget{[]string{"consolidation"}, "synthetic"},
		commandTarget{[]string{"compaction"}, "synthetic"},
		commandTarget{[]string{"commitments"}, "synthetic"},
		commandTarget{[]string{"coordinator"}, "synthetic"},
	)
	if n := len(commandCandidates("c")); n < 10 {
		t.Fatalf("synthetic table yielded only %d candidates", n)
	}
	if got := lipgloss.Width(cmdLine(t, 60, "c")); got > 60 {
		t.Fatalf("command line rendered %d columns, want <= 60", got)
	}
}
