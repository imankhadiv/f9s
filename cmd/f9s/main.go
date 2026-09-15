// f9s is a terminal UI for Apache Flink®: manage jobs, diagnose checkpoints
// and backpressure, and read exceptions without leaving the terminal.
//
// f9s is an independent project, not affiliated with the Apache Software
// Foundation. Apache Flink is a trademark of the ASF.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/imankhadiv/f9s/internal/ai"
	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/doctor"
	"github.com/imankhadiv/f9s/internal/eval"
	"github.com/imankhadiv/f9s/internal/mcpserver"
	"github.com/imankhadiv/f9s/internal/model"
	"github.com/imankhadiv/f9s/internal/plugin"
	"github.com/imankhadiv/f9s/internal/sink"
	"github.com/imankhadiv/f9s/internal/ui"
	"github.com/imankhadiv/f9s/internal/ui/styles"
	"github.com/imankhadiv/f9s/internal/watchd"
)

var version = "dev" // set by goreleaser ldflags

func main() {
	if len(os.Args) > 1 && os.Args[1] == "init" {
		runInit()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		runDoctor(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "eval" {
		runEval(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "watch" {
		runWatch(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		runMCP(os.Args[2:])
		return
	}
	var (
		flagConfig   = flag.String("config", config.DefaultPath(), "path to config file")
		flagContext  = flag.String("context", "", "named context from the config file")
		flagAddress  = flag.String("address", "", "Flink REST address, overrides config (e.g. http://localhost:8081)")
		flagRefresh  = flag.Int("refresh", 0, "refresh interval in seconds, overrides config")
		flagSnapshot = flag.Bool("snapshot", false, "print the jobs table once and exit (non-interactive)")
		flagReadOnly = flag.Bool("read-only", false, "disable every mutating action, regardless of context config")
		flagVersion  = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *flagVersion {
		fmt.Println("f9s", version)
		return
	}

	cfg, err := config.Load(*flagConfig)
	if err != nil {
		fail(err)
	}
	if *flagReadOnly {
		for name, cctx := range cfg.Contexts {
			cctx.ReadOnly = true
			cfg.Contexts[name] = cctx
		}
	}
	plugins, alertHook, err := plugin.Load(plugin.DefaultPath())
	if err != nil {
		fail(err)
	}
	if err := styles.LoadSkin(styles.DefaultSkinPath()); err != nil {
		fail(err)
	}
	name, cctx, err := cfg.Resolve(*flagContext)
	if err != nil {
		fail(err)
	}
	if *flagAddress != "" {
		name, cctx = "flag", config.Context{URL: *flagAddress}
		cfg.Contexts[name] = cctx
	}
	refresh := cfg.RefreshRate
	if *flagRefresh > 0 {
		refresh = *flagRefresh
	}

	cl := client.New(cctx)

	if *flagSnapshot {
		if err := snapshot(cl); err != nil {
			fail(err)
		}
		return
	}

	prog := tea.NewProgram(ui.New(cfg, name, refresh, plugins, alertHook), tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fail(err)
	}
}

// snapshot prints the jobs table once; useful for scripting and smoke tests.
func snapshot(cl *client.Client) error {
	ov, err := cl.Overview()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	jobs, err := cl.Jobs()
	if err != nil {
		return err
	}
	fmt.Printf("flink %s · taskmanagers %d · slots %d/%d\n",
		ov.FlinkVersion, ov.TaskManagers, ov.SlotsAvailable, ov.SlotsTotal)
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tJID\tSTATUS\tUPTIME\tTASKS")
	for _, j := range jobs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d/%d\n",
			j.Name, model.ShortJID(j.JID), j.State,
			model.FmtDuration(j.Duration), j.Tasks.Running, j.Tasks.Total)
	}
	return w.Flush()
}

// runMCP serves the f9s MCP server over stdio.
func runMCP(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	var (
		flagConfig  = fs.String("config", config.DefaultPath(), "path to config file")
		flagContext = fs.String("context", "", "named context from the config file")
		flagWrites  = fs.Bool("allow-writes", false, "register mutating tools (savepoint/stop/cancel)")
	)
	_ = fs.Parse(args)
	cfg, err := config.Load(*flagConfig)
	if err != nil {
		fail(err)
	}
	if err := mcpserver.Run(cfg, *flagContext, version, *flagWrites); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "f9s:", err)
	os.Exit(1)
}

const starterConfig = `# f9s configuration — contexts are Flink clusters you switch between (C).
refreshRate: 2
currentContext: local
contexts:
  local:
    url: http://localhost:8081
    # kubeContext: docker-desktop                # required for the FlinkDeployments view (K)
    # savepointDir: s3://my-bucket/savepoints   # prefills the savepoint prompt
    # sqlGateway: http://localhost:8083         # enables :sql
    # historyServer: http://localhost:8082      # enables :history
    # ai:                                       # or press 'a' in the TUI to set up interactively
    #   provider: ollama                        # ollama | openai | anthropic
    #   url: http://localhost:11434
    #   model: qwen3:8b
    #   tokenEnv: MY_API_KEY_ENV                # env var NAME — keys never live in this file
    #   watch: true                             # auto-investigate alerts
  # prod:
  #   url: https://flink.prod.example.com
  #   kubeContext: prod-us                       # never falls back to kubectl's current context
  #   auth: { type: bearer, tokenEnv: FLINK_PROD_TOKEN }
`

// runInit writes a commented starter config, refusing to overwrite.
func runInit() {
	path := config.DefaultPath()
	if _, err := os.Stat(path); err == nil {
		fmt.Println("config already exists:", path)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(path, []byte(starterConfig), 0o600); err != nil {
		fail(err)
	}
	fmt.Println("wrote", path)
	fmt.Println("edit it, or just run f9s — every commented block is optional.")
}

// runDoctor lints the cluster and prints a colorized pass/warn/fail report.
func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	flagConfig := fs.String("config", config.DefaultPath(), "path to config file")
	flagContext := fs.String("context", "", "named context from the config file")
	flagAddress := fs.String("address", "", "Flink REST address, overrides config")
	_ = fs.Parse(args)
	cfg, err := config.Load(*flagConfig)
	if err != nil {
		fail(err)
	}
	name, cctx, err := cfg.Resolve(*flagContext)
	if err != nil {
		fail(err)
	}
	if *flagAddress != "" {
		cctx.URL = *flagAddress
	}
	cl := client.New(cctx)
	findings, err := doctor.Run(cl)
	if err != nil {
		fail(err)
	}
	fmt.Printf("f9s doctor · context %s · %s\n\n", name, cctx.URL)
	worst := doctor.SevOK
	for _, f := range findings {
		glyph, color := "✓", "\033[32m"
		switch f.Sev {
		case doctor.SevCritical:
			glyph, color = "✗", "\033[31m"
		case doctor.SevWarn:
			glyph, color = "⚠", "\033[33m"
		case doctor.SevInfo:
			glyph, color = "ℹ", "\033[36m"
		}
		if f.Sev > worst {
			worst = f.Sev
		}
		fmt.Printf("%s%s %-4s\033[0m %-42s %s\n", color, glyph, f.Sev, f.Area, f.Text)
		if f.Remedy != "" {
			fmt.Printf("       %-42s \033[2m↳ %s\033[0m\n", "", f.Remedy)
		}
	}
	if worst >= doctor.SevCritical {
		os.Exit(2)
	}
	if worst >= doctor.SevWarn {
		os.Exit(1)
	}
}

// runEval scores the context's AI provider on frozen incident scenarios.
func runEval(args []string) {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	flagConfig := fs.String("config", config.DefaultPath(), "path to config file")
	flagContext := fs.String("context", "", "named context (its ai block picks the provider)")
	flagDir := fs.String("scenarios", "evals/scenarios", "scenario directory")
	flagJSON := fs.Bool("json", false, "emit the report as JSON")
	_ = fs.Parse(args)
	cfg, err := config.Load(*flagConfig)
	if err != nil {
		fail(err)
	}
	_, cctx, err := cfg.Resolve(*flagContext)
	if err != nil {
		fail(err)
	}
	provider, err := ai.New(cctx.AI)
	if err != nil {
		fail(fmt.Errorf("this context has no usable ai block: %w", err))
	}
	agent, ok := ai.AsAgent(provider)
	if !ok {
		fail(fmt.Errorf("provider %s has no agentic support", provider.Name()))
	}
	scenarios, err := eval.LoadScenarios(*flagDir)
	if err != nil {
		fail(err)
	}
	fmt.Printf("f9s eval · provider %s · %d scenarios\n\n", provider.Name(), len(scenarios))
	var results []eval.Result
	correct := 0
	for _, sc := range scenarios {
		r := eval.Run(sc, agent)
		results = append(results, r)
		if r.Correct {
			correct++
		}
		mark := "✗"
		if r.Correct {
			mark = "✓"
		}
		extra := ""
		if r.Halluc {
			extra += " HALLUCINATED"
		}
		if r.Proposals > 0 {
			extra += fmt.Sprintf(" proposals=%d", r.Proposals)
		}
		if r.Err != "" {
			extra += " err=" + r.Err
		}
		fmt.Printf("%s %-26s steps=%d %5.1fs%s\n    %s\n", mark, sc.Name, r.Steps, r.DurationS, extra, r.Hypothesis)
	}
	fmt.Printf("\nscore: %d/%d correct\n", correct, len(results))
	if *flagJSON {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
	}
	if correct < len(results) {
		os.Exit(1)
	}
}

// runWatch runs the headless collector: NDJSON alert events to stdout and,
// optionally, a Prometheus metrics endpoint.
func runWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	flagConfig := fs.String("config", config.DefaultPath(), "path to config file")
	flagContext := fs.String("context", "", "named context from the config file")
	flagAddress := fs.String("address", "", "Flink REST address, overrides config")
	flagJSON := fs.Bool("json", false, "emit NDJSON alert events to stdout")
	flagMetrics := fs.String("metrics-addr", "", "serve Prometheus metrics at this address (e.g. :9090)")
	flagInterval := fs.Int("interval", 15, "poll interval in seconds")
	_ = fs.Parse(args)
	cfg, err := config.Load(*flagConfig)
	if err != nil {
		fail(err)
	}
	name, cctx, err := cfg.Resolve(*flagContext)
	if err != nil {
		fail(err)
	}
	if *flagAddress != "" {
		name, cctx = "flag", config.Context{URL: *flagAddress}
	}
	emit := func(watchd.Event) {}
	if *flagJSON {
		emit = watchd.EncodeNDJSON(os.Stdout)
	}
	cl := client.New(cctx)
	col := watchd.New(cl, name, time.Duration(*flagInterval)*time.Second, emit)
	if len(cctx.Sinks) > 0 {
		sinks, err := sink.Build(cctx.Sinks)
		if err != nil {
			fail(err)
		}
		col.WithSinks(sinks)
		fmt.Fprintf(os.Stderr, "f9s watch: %d alert sink(s) attached\n", len(sinks))
	}

	if *flagMetrics != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			col.WriteMetrics(w)
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "f9s watch · context %s · %s\nGET /metrics\n", name, cctx.URL)
		})
		go func() {
			if err := http.ListenAndServe(*flagMetrics, mux); err != nil {
				fail(err)
			}
		}()
		fmt.Fprintf(os.Stderr, "f9s watch: metrics on %s/metrics, polling %s every %ds\n", *flagMetrics, cctx.URL, *flagInterval)
	} else if !*flagJSON {
		fmt.Fprintln(os.Stderr, "f9s watch: nothing to emit — pass --json and/or --metrics-addr")
		os.Exit(2)
	}

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; close(stop) }()
	col.Run(stop)
}
