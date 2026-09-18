# f9s vs the Flink Web UI

An honest comparison. The Web UI is fine for a first look at a healthy
job — f9s exists for everything after that.

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
| Completed-job postmortems | separate History Server UI in a browser | ✓ `:history`, same keybindings |
| SQL console | ✗ | ✓ `Q` via SQL Gateway |
| Kubernetes operator awareness | ✗ | ✓ `K` FlinkDeployments |
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
> savepoint path it takes, because Flink doesn't. **Savepoint,
> stop-with-savepoint, cancel and rescale** always confirm before they touch
> the cluster, and `readOnly: true` disables them outright. Expect the UI to
> keep moving while it's 0.x — the roadmap lives in the [README](../README.md).

## What the Web UI simply doesn't have

- **Actions**: savepoint, stop-with-savepoint, live FLIP-291 rescale, jar
  submit + restore — the Web UI can cancel a job (unauthenticated!) and
  nothing else.
- **Detection**: hysteresis alarms for backpressure, data skew,
  checkpoint-failure streaks, restart loops; a pre-backpressure early
  warning from soft-backpressure + mailbox latency; blast-radius
  correlation across jobs.
- **Memory**: savepoint ledger, per-job config journal ("what changed?"),
  job archaeology across the History Server, incident timeline, one-key
  postmortem export, AI incident memory.
- **Safety**: restore preflight that names the state a wrong jar would
  drop; read-only mode; an audit trail of every mutating action;
  AI evidence anonymization with a redaction preview.
- **Diagnosis**: computed bottleneck + parallelism advice with slot math,
  skew cause classifier with fixes, checkpoint straggler breakdown,
  `f9s doctor` cluster lint, flame graphs and thread dumps one key away,
  and an agentic AI investigator whose every tool call is shown.

## What the Web UI has that f9s deliberately skips

- The graph SVG (f9s renders the same topology as greppable ASCII with
  live rates painted on the edges)
- The metrics chart picker (superseded by the fuzzy Metrics Explorer)
- Accumulators (legacy DataSet API; rarely populated today)

## Robustness under fire

Point both at a degraded cluster: the Web UI white-screens; f9s caps its
request concurrency, backs off exponentially, marks stale rows, explains
the failure, and stays interactive. A monitoring tool must never become
part of the outage.

*Every capability above ships today — try `make chaos` and see for yourself.*
