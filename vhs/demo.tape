# f9s demo — render with: vhs vhs/demo.tape
# Requires the playground running: make playground
Output vhs/demo.gif

Set FontSize 15
Set Width 1200
Set Height 640
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Type "./f9s"
Enter
Sleep 4s

# jobs view: checkpoint health, skew, triage sort
Sleep 2s

# drill in: vertices, backpressure glyphs, busy%, watermark lag, skew skyline
Enter
Sleep 4s

# skew drill-down: distribution + cause + fix
Type "d"
Sleep 4s
Escape
Sleep 1s

# ASCII execution DAG with live busy/backpressure
Type "g"
Sleep 4s
Escape
Sleep 1s

# merged per-job logs — kubectl logs for a Flink job
Type "L"
Sleep 3s
Escape
Sleep 1s

# checkpoints deep-dive: config line + state-size trend
Type "c"
Sleep 3s
Escape
Sleep 1s
Escape
Sleep 1s

# command mode → TaskManagers: GC/s column, then the memory-model panel
Type ":"
Sleep 1s
Type "tm"
Sleep 1s
Enter
Sleep 3s
Enter
Sleep 4s
Escape
Sleep 1s
Escape
Sleep 1s

# jar submitter: upload / run / restore from ledger savepoints
Type ":"
Sleep 1s
Type "jars"
Sleep 1s
Enter
Sleep 3s
Escape
Sleep 1s

# incident timeline: alerts, actions, investigations, merged
Type ":"
Sleep 1s
Type "tl"
Sleep 1s
Enter
Sleep 4s
Escape
Sleep 1s

# doctor: config smells before they page you
Type ":"
Sleep 1s
Type "doctor"
Sleep 1s
Enter
Sleep 4s
Escape
Sleep 1s

# ctrl-p action palette
Ctrl+P
Sleep 1s
Type "archae"
Sleep 2s
Escape
Sleep 1s

# savepoint ledger and help
Type "p"
Sleep 3s
Escape
Sleep 1s
Type "?"
Sleep 3s

Type "q"
Sleep 1s
Type "q"
Sleep 1s
