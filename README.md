# f9s

**A terminal UI for [Apache Flink®](https://flink.apache.org)** — k9s for your streaming jobs.

Watch jobs, drill into vertices with live backpressure levels, diagnose checkpoint
failures, and read root exceptions — without leaving the terminal or clicking
through the Web UI.

![f9s demo](vhs/demo.gif)

**At-a-glance cluster health** — `:pulse` folds slots, job mix, throughput,
backpressure, checkpoints, and alerts into one panel with a single verdict:

![Cluster pulse dashboard](vhs/dashboard.png)

**The Flink Web UI job graph, in your terminal** — press `g` on a job for
the execution graph as health-colored operator boxes (parallelism,
backpressure/busy/skew per box, `FORWARD`/`HASH` edges):

![Execution graph boxes](vhs/dag.png)

## Why not just the Flink Web UI?

The dashboard is fine at watching one job. Operating is a different sport:

| | Flink Web UI | f9s |
|---|---|---|
| Trigger a savepoint | ✗ REST/CLI only | ✓ `s`, with confirmation |
| Stop-with-savepoint | ✗ | ✓ `S` |
| Remember savepoint paths | ✗ (your shell history) | ✓ the ledger, with restore picker |
| Multi-cluster | ✗ one UI per JobManager | ✓ fleet view + context switch |
| Keyboard-driven / over SSH | ✗ browser + port-forward | ✓ it's a terminal |
| Backpressure + busy + watermarks | spread across tabs, click per vertex | ✓ one table, bottleneck auto-flagged |
| Which subtask is actually blocked | ✗ vertex-level only | ✓ `e` per-subtask buffer fingerprint |
| Checkpoint history | last **10** (`web.checkpoints.history`) | ✓ full history view |
| Which operator made checkpoints slow/big | ✗ open each checkpoint, compare by eye | ✓ `c` per-vertex compare of the last 6, top grower named |
| Will a cancel lose my state? | ✗ read the config yourself | ✓ retention + failure-budget warnings on the checkpoints view |
| JVM fleet: heap, GC time, metaspace, buffers | ✗ one TaskManager page at a time | ✓ `:fleet`, trended, OOM precursors flagged |
| Completed-job postmortems | separate History Server UI in a browser | ✓ `:history`, same keybindings |
| SQL console | ✗ | ✓ `Q` via SQL Gateway |
| Kubernetes operator awareness | ✗ | ✓ `K` operator tree: session jobs, drift, events, pods, guarded CR changes |
| Autoscaler decisions | ✗ | ✓ current ⇒ recommended parallelism on the graph, flapping flagged |
| AI diagnosis & chat | ✗ | ✓ explain `a`, investigate `A`, ask `@` — local-first, anonymized |
| AI-suggested actions | ✗ | ✓ proposed, never auto-run — you confirm inline |
| Page-aware AI panel | ✗ | ✓ `A` — a side panel that always knows the page you're on |
| Agent access | ✗ | ✓ `f9s mcp` |
| Extensible | rebuild the Angular app | ✓ `plugins.yaml` |

> "Built-in Flink UI is extremely useful when dealing with a single job —
> but what if you have 10, 20 or 100 jobs?" — every Flink operator, eventually

> ⚠️ **Status: pre-1.0.** The daily loop is solid: jobs, job detail and the
> execution graph, checkpoints, exceptions, TaskManagers, the metrics explorer,
> multi-cluster contexts, and the **savepoint ledger** — f9s records every
> savepoint path it takes, because Flink doesn't. **Every mutating action** —
> savepoint, stop, cancel, rescale, savepoint restore/disposal, and Flink
> Kubernetes Operator changes — confirms before it touches the cluster, is
> written to an audit trail, and is disabled outright by `readOnly: true`. Expect the UI to
> keep moving while it's 0.x — see the roadmap below.

## Quickstart

The fastest way to see what f9s does — no Flink required, just Docker:

```sh
git clone https://github.com/imankhadiv/f9s && cd f9s
make chaos     # local cluster + broken-on-purpose jobs
make run       # every detector fires within ~90s: press ! for alerts,
               # enter a red job, d for the skew story, :timeline for the feed
```

Against your own cluster:

```sh
go install github.com/imankhadiv/f9s/cmd/f9s@latest   # Go 1.27+
f9s init     # optional: writes a commented starter config
f9s doctor   # lint a cluster for the config smells that page you later
f9s eval     # score your AI provider on frozen incident scenarios
f9s --address http://localhost:8081
f9s --address http://localhost:8081 --snapshot   # non-interactive, for scripts
```

Connection failed? f9s diagnoses instead of stack-tracing — and keeps
retrying with backoff until the cluster answers.

## Keybindings

| Key | Action |
|-----|--------|
| `enter` | drill into selected job (vertices + backpressure + a pre-backpressure early warning from soft-bp/mailbox-latency) |
| `l` | logs, k9s-style: JobManager log from the jobs view; a TaskManager's log from the `:tm` list — `F` lists rotated files, `o` tails stdout |
| `L` | merged job logs: tails every TaskManager running the job's subtasks, timestamp-merged with per-TM tags — kubectl-logs for a Flink job |
| `c` | checkpoints for selected job — incremental upload size per checkpoint (DELTA), in-flight data, what a cancel would do to retained checkpoints, and the tolerable-failure budget; `enter` on a checkpoint opens the per-subtask straggler breakdown with a blocked-share heatmap; `c` compares it with the previous completed checkpoints vertex by vertex (`m` cycles size/duration/uploaded/alignment); a failed run carries its reason in a banner instead of a bare `FAILED` |
| `x` | exceptions for selected job |
| `s` | trigger savepoint (prompts for target dir) |
| `S` | stop with savepoint |
| `ctrl-x` | cancel job |
| `p` | savepoint ledger (`enter` restore cmd, `y` copy path, `D` dispose — refuses a running job's only recovery point) |
| `o` | cycle sort: triage / name / uptime / status |
| `t` | TaskManagers (enter → heap/GC detail, `l` log tail, `P` flame graph, `T` thread dump; on a shared session cluster a JOBS column and a per-job slot-share line) (in the jobs view) |
| `m` | metrics explorer: fuzzy-search every cluster metric (`tm:`/`job:`/`v:` prefixes jump scope), inspect live, `s` per-subtask bars that refresh with the value, `ctrl-p` pins a tile (persisted per context, with a sparkline sampled even while the explorer is closed; a fresh context starts with curated pins), `a` asks AI about the value. Metric names carrying Flink's reserved characters (`# $ & + / ; = ? @`) resolve correctly |
| `C` | contexts fleet view (enter → switch cluster) |
| `a` | AI explain: diagnose the selected job (see below) |
| `A` | agentic investigate: the model drills deeper with read-only tools, every call shown |
| `@` | ask AI: free-form chat about the job (or `:ask` cluster-wide) — starter questions, and it can propose actions you approve inline |
| `b` | backpressure history sparklines (in job detail) |
| `e` | the backpressure edge: a vertex's per-subtask buffer fingerprint — which subtasks are actually blocked, not just which vertex looks busy (in job detail) |
| `g` | execution graph as Flink-UI-style boxes — per-operator parallelism, backpressure/busy/skew gauges, health-colored; each box shows the next parallelism (`⇒ N●` operator autoscaler decision, `⇒ N◐` its recommendation, `⇒ N~` f9s's own utilization estimate); `←→` selects a box, `enter` opens the live inspector for that vertex (rates, busy/backpressure, watermark lag, buffers, per-subtask bars and 5-minute sparklines — plus a `rows` tab that tails the stdout of the TaskManagers running *that* vertex, for jobs that print), `R` rescales it (FLIP-291), `O` pins its parallelism in the FlinkDeployment, `shift+←→` pans (in job detail) |
| `t` | per-vertex subtask table: attempts, TaskManager, lifecycle durations; enter shows retained attempt history (in job detail) |
| `O` | config resolution: the cluster→job→operator override chain per setting, with the winning layer marked (in job detail) |
| `w` | watermark propagation debugger: per-vertex event-time lag, the first stalling vertex + why (job detail) |
| `W` | what changed: the job's config journal — every parallelism/checkpoint/restart-strategy change ever observed, also flashed live and threaded into `:timeline` |
| `R` | rescale vertex live (FLIP-291, needs adaptive scheduler) |
| `d` | skew drill-down: per-subtask distribution + cause + fix |
| `Q` | SQL Gateway console (needs `sqlGateway:` in the context) |
| `K` | Flink Kubernetes Operator tree (via kubectl; needs `kubeContext:` in the context) — see [below](#kubernetes-operator) |
| `/` | filter jobs |
| `:` | command mode — a dropdown lists the matching commands with their descriptions as you type (`:` alone shows all of them); `↑↓` selects, `tab` completes, `enter` runs the highlighted one. Commands: `:jobs` `:cluster` `:tms` `:savepoints` `:contexts` `:k8s` `:sql` `:log` `:alerts` `:investigations` `:config` `:jars` `:jmlog` `:timeline` `:doctor` `:ask` `:pulse` `:waste` `:fleet` `:history` `:quit`, plus short aliases (`:j` `:sp` `:tm` `:ctx`). A prefix matching several destinations names them rather than guessing: `:c` answers `cluster · contexts · log · config · ask · waste` instead of silently picking one |
| `:timeline` | the merged incident feed — alerts, actions, config changes, operator autoscaler decisions (with flapping flagged), AI investigations, chronologically; `E` exports a markdown postmortem draft |
| `:fleet` | every JobManager/TaskManager JVM: heap, GC share of wall time, metaspace, managed memory, network buffers — 5-minute sparklines, OOM precursors flagged |
| `!` | alerts (sentinel: backpressure, data skew, checkpoint-failure streaks, restart loops — hysteresis-gated). Telemetry f9s couldn't read counts as unknown, not healthy, so a monitoring gap pauses the hysteresis instead of quietly resolving your incident |
| `:jars` | upload & submit jars; `S` restores from a savepoint (prefilled from the ledger) — the stop→fix→resume loop without leaving the terminal. Restores are **strict**: see below |
| `r` | refresh now |
| `esc` | back |
| `?` | help |
| `q` | quit |

## AI: explain, investigate, and ask

Three ways in, all driven by *your* model (local-first via Ollama, or any
OpenAI-compatible / Anthropic endpoint):

- **`a` — explain**: a one-shot diagnosis. f9s gathers a small evidence bundle
  (checkpoint history, backpressure, computed bottleneck, exceptions), anonymizes
  it (job names, hosts, storage paths → placeholders), sends it to your model,
  and renders the diagnosis with the evidence underneath so you can verify it.
- **`A` — investigate**: the model drills deeper with read-only tools, every
  call shown live — the audit trail *is* the UI.
- **`@` — ask** (`:ask` for cluster-wide): a free-form, multi-turn chat. An
  empty chat offers tappable starter questions; ask a follow-up and the model
  answers, grounding itself in the job's evidence and calling read-only tools
  as needed.

An empty chat suggests where to start:

![AI ask — starter questions](vhs/ai-ask-starters.png)

Ask a real question and get a grounded answer:

![AI ask — diagnosis](vhs/ai-ask-answer.png)

**Nothing runs without you.** When the model wants a mutating action
(savepoint, stop, cancel) it *proposes* — it never executes. Approve it inline
with one keystroke, and f9s shows the exact call for a final confirmation
before anything happens:

![AI proposes an action](vhs/ai-propose.png)
![Human-in-the-loop confirmation](vhs/ai-propose-confirm.png)

**A page-aware AI side panel** — press `A` on any page for a chat that already
knows what you're looking at (`AI · knows: job / checkpoints / cluster …`),
grounded in the on-screen evidence:

![Page-aware AI panel](vhs/ai-panel.png)

Every action taken this way lands in the audit log (`~/.local/share/f9s/audit/`),
and a `readOnly` context refuses AI proposals outright.

```yaml
contexts:
  prod-eu:
    ai: { policy: deny }              # nothing ever leaves this cluster
  staging:
    ai: { provider: ollama, model: qwen3, watch: true }  # local-first; watch auto-investigates alerts
    # …or skip the file entirely: press `a` on any job and f9s opens an
    # in-TUI setup form (provider/url/model/token-env), saves it, and
    # retries your action. Keys are never stored — only env-var names.
    # local floor: qwen3:8b — smaller models lose the tool-call loop. Uses Ollama's native /api/chat.
    # Every investigation's audit trail lands in ~/.local/share/f9s/investigations/
  dev:
    ai: { provider: anthropic, tokenEnv: ANTHROPIC_API_KEY }
    # or any OpenAI-compatible endpoint:
    # ai: { provider: openai, url: "https://gateway.corp/v1", model: gpt-x, tokenEnv: LLM_TOKEN }
```

## MCP server — bring your own agent

Any MCP client (Claude Code, Claude Desktop, Cursor, …) can operate Flink
through f9s:

```sh
f9s mcp --context staging                 # read-only tools
f9s mcp --context staging --allow-writes  # + savepoint / stop / cancel
```

Read tools: `list_jobs`, `get_job`, `get_checkpoints`, `get_exceptions`,
`get_taskmanagers`, `list_savepoints`, `get_signal_history`, and
`get_diagnosis_evidence` (f9s's curated analyzer bundle). Write tools exist
only when the operator passes `--allow-writes`: savepoint, stop, cancel,
rescale, restore-from-savepoint and savepoint disposal — plus, when the
context has a `kubeContext`, the operator tools (suspend/resume, restart,
savepoint, parallelism, redeploy from savepoint, per-vertex parallelism pins,
autoscaler on/off and target utilization). Savepoints taken through MCP land
in the ledger too, and every write is audited.

Claude Code registration: `claude mcp add f9s -- f9s mcp --context staging`

## Capacity

`:waste` ranks running jobs by busy-time-weighted slot utilization —
a job holding 4 slots at 6% busy is burning ~3.8 idle slot-equivalents;
set `costPerSlotHour` and it attributes the idle $/day per job. Reclaim
candidates (low busy, multiple slots) show in red.

Set `stateVolumeGB: <size>` on a context and the checkpoints view turns
the state-size trend into a disk-full ETA — least-squares over completed
checkpoints projects when growing state fills the volume, red under a
week out. Without it you still get the quantified growth rate.

`:fleet` is the JVM side of capacity: heap, GC time as a share of wall
time, metaspace (steady growth on a session cluster is a classloader leak),
managed memory and network buffers for every JobManager and TaskManager —
sampled every 15s into f9s's metric history, which survives restarts.

## Kubernetes operator

With `kubeContext:` set on a context, `K` shows the Flink Kubernetes
Operator's resources as a tree — each FlinkDeployment with its
FlinkSessionJobs nested beneath — with lifecycle, reconcile state (`⟳` when a
spec edit hasn't been reconciled yet) and the autoscaler mode (`●` scaling,
`◐` recommendations only, `○` off). `enter` opens a resource: the decoded
operator error, job spec, savepoint history and autoscaler settings; `e`
lists its Kubernetes events, `p` its pods (restarts, `OOMKilled`,
`CrashLoopBackOff`), `y` the raw YAML.

From the detail view, `s` savepoint · `z` suspend/resume · `x` restart · `P`
parallelism · `b` redeploy from a savepoint · `o` autoscaler on/off · `u`
target utilization. Every change is a JSON merge patch that f9s:

1. refuses while the operator is upgrading or rolling back, or while an
   earlier spec edit is still unreconciled;
2. validates with `kubectl patch --dry-run=server` (a field your operator
   version doesn't know fails here, before anything changes);
3. applies only after your confirmation, and audits.

f9s always passes `--context <kubeContext>` and never falls back to kubectl's
current context.

## Desktop notifications

Set `notify: true` on a context and f9s emits an OSC 9 desktop
notification + terminal bell the moment an alert opens — so a
backgrounded terminal still reaches you (iTerm2, kitty, WezTerm, and
other OSC 9-aware terminals).

## Trust & governance

- `--read-only` (or `readOnly: true` per context): every mutating action
  disabled, `[RO]` badge always visible — hand it to anyone on call. The
  restriction survives `--address`; the configured context's credentials
  deliberately do not, since a typed address may be another cluster.
- `readOnly` is a ceiling, not a default: `f9s mcp --allow-writes` against a
  read-only context refuses to start rather than quietly serving write tools,
  so a command-line opt-in can't widen a policy the config set.
- **Restores fail closed.** Restoring a jar from a savepoint sends
  `allowNonRestoredState: false`, so Flink rejects a savepoint whose operators
  don't map instead of silently dropping that state. f9s first compares the
  jar's plan against the savepoint's originating job and names the operators
  that would be lost; dropping them takes a second, deliberate confirmation of
  the same jar and path.
- Every mutating action is audited to `~/.local/share/f9s/audit/<ctx>.jsonl`:
  who, when, what, against which target, with what result — TUI keys, MCP
  tools, and AI-approved proposals alike.
- AI evidence is anonymized by default, credentials/hostnames are scrubbed
  even inside stack traces, and `ai.preview: true` shows the exact outbound
  text before anything is sent.

## Headless mode — f9s as infrastructure

`f9s watch` runs the same detectors as the TUI with no terminal — the
analysis engine as a Unix citizen and a Prometheus target:

```sh
# stream alert events as NDJSON (pipe into jq, a Slack bot, Fluent Bit…)
f9s watch --json | jq 'select(.kind=="skew")'

# expose f9s's COMPUTED verdicts to Prometheus/Grafana
f9s watch --metrics-addr :9090        # then scrape :9090/metrics
```

Emitted metrics are f9s's own analysis, not raw Flink metrics:
`f9s_vertex_skew_percent`, `f9s_vertex_backpressure_level` (0/1/2),
`f9s_vertex_busy_ms_per_sec`, `f9s_job_restarts`, `f9s_job_has_bottleneck`,
`f9s_job_open_alerts`, plus `f9s_up` / `f9s_scrape_age_seconds`. NDJSON
events carry a `schemaVersion` so integrations don't break on upgrades.

`f9s_up` means the sweep, not the process: it is 1 only when the job list
*and* every monitored job's detail were read, and 0 the moment either fails —
so alert on it. `f9s_scrape_age_seconds` ages from the last complete sweep and
is absent until the first one, rather than reporting a scrape that never
happened. A job f9s couldn't read keeps its last-good gauges instead of
vanishing from the scrape, and the rest of the cluster is still collected.

### Alert sinks

Forward opened/cleared alerts straight to where your team already looks —
add a `sinks:` list to any context (works in the TUI's watch mode and in
`f9s watch`):

```yaml
contexts:
  prod:
    url: https://flink.prod:8081
    sinks:
      - type: slack          # or: webhook | alertmanager | pagerduty
        url: https://hooks.slack.com/services/...
        minKind: skew        # optional: only this alert kind
        openOnly: true       # optional: skip the "cleared" events
```

One installs f9s into a rotation: an SRE adds the webhook once, and the
whole team sees "f9s caught this" in `#flink-alerts` — no terminal
required. A dead sink never blocks the others.

## Skins

Three skins ship built in — `F9S_SKIN=catppuccin` (Latte/Mocha),
`F9S_SKIN=high-contrast`, and `F9S_SKIN=colorblind` (Okabe-Ito: ok/warn/error
stay distinguishable under deuteranopia and protanopia).

Or override the palette yourself in `~/.config/f9s/skin.yaml` (or point
`F9S_SKIN` at a file). Values are hex, an ANSI 0-255 index, or a light/dark
pair:

```yaml
colors:
  accent: "#E6526F"
  ok: {light: "#004400", dark: "#88FF88"}
  warn: "214"
  flow: {light: "#2563EB", dark: "#5B9BF5"}
  state: {light: "#734591", dark: "#BF73F2"}
  backpressure: {light: "#171717", dark: "#E5EAF4"}
```

Tokens: `accent`, `ok`, `warn`, `error`, `dim`, `selectionFg`, `selectionBg`,
`flow`, `state`, `backpressure`. The defaults use Apache Flink's coral,
orange, purple, and neutral palette; graph colors keep Flink's operational
meaning of blue flow, red-hot work, and dark backpressure.

f9s uses the terminal's font rather than choosing one itself. For aligned
gauges and graph boxes, use a modern monospace font with complete box-drawing
glyphs, such as JetBrains Mono, IBM Plex Mono, or Iosevka. Ligatures are
optional; f9s never relies on them for meaning.

## Plugins

Bind your own commands to keys — the selected job arrives as JSON on stdin:

```yaml
# ~/.config/f9s/plugins.yaml
plugins:
  explain:
    key: "e"
    description: "explain with AI"
    command: "sh"
    args: ["-c", "jq -r .job.name | xargs -I{} echo analyzed {}"]
    background: true   # false suspends the TUI for interactive commands
```

`F9S_CONTEXT` and `F9S_URL` are set in the plugin's environment.

## Configuration

Contexts are kubeconfig-shaped. Optional — with no config file, f9s connects
to `http://localhost:8081`.

```yaml
# ~/.config/f9s/config.yaml
refreshRate: 2
currentContext: prod-eu
contexts:
  prod-eu:
    url: https://flink.prod.example.com
    kubeContext: prod-eu-admin                  # enables the operator tree (K), autoscaler overlay and operator actions
    auth: { type: bearer, tokenEnv: FLINK_TOKEN }
    savepointDir: s3://my-bucket/savepoints   # prefills the savepoint prompt
    sqlGateway: https://flink-gw.prod.example.com   # enables the SQL console (Q)
    historyServer: https://flink-hs.prod.example.com # enables :history postmortems
    # readOnly: true                          # view-only: disables every mutating action
  local:
    url: http://localhost:8081
```

Flink has no built-in auth; if your cluster sits behind a proxy, use
`auth` (bearer/basic) or arbitrary `headers` per context. `tlsSkipVerify: true`
is available for lab clusters. The FlinkDeployment view requires an explicit
`kubeContext` on the selected f9s context; f9s never inherits kubectl's current
context.

## Roadmap

- **v0.1** — ✅ actions + savepoint ledger (done); computed backpressure
  root-cause; restore-from-savepoint picker.
- **v0.2** — TaskManager views + logs, watermark columns, multi-cluster fleet
  view, exec-plugin system, Homebrew tap.
- **v0.3+** — AI explain (local-first via Ollama, k8sgpt-style analyzers),
  `f9s mcp` server mode, Flink Kubernetes Operator awareness, SQL Gateway console.
- **Pre-1.0 depth** — ✅ backpressure edge, checkpoint health and per-vertex
  compare, operator tree with guarded CR changes, autoscaler overlay and
  flapping detection, JVM fleet view, persistent metric history.
- **Next** — Homebrew tap, public launch.

## Contributing

Early days — issues and feedback are very welcome. Please open an issue before
sending a PR. If you used AI assistance for a contribution, say so in the PR;
low-effort AI-generated PRs will be closed without review.

## License

[Apache-2.0](LICENSE).

f9s is an independent project, not affiliated with or endorsed by the Apache
Software Foundation. Apache Flink, Flink®, and the squirrel logo are
[trademarks](https://www.apache.org/foundation/marks/) of the Apache Software
Foundation.
