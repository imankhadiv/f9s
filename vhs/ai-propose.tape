# f9s AI proposal + inline approval demo — render with: vhs vhs/ai-propose.tape
# Requires: playground running + an AI provider on the local context (ollama).
# Produces stills used in the README: ai-propose.png, ai-propose-confirm.png.
# The tape DECLINES the action (esc), so the playground job is never stopped.
Output vhs/ai-propose.gif

Set FontSize 15
Set Width 1200
Set Height 640
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms

Type "./f9s --config vhs/ai-demo-config.yaml"
Enter
Sleep 4s

# scope a chat to the backpressured job
Type "/"
Type "chaos_sink"
Enter
Sleep 1s
Type "@"
Sleep 2s

# ask for a mutating action — the AI never executes; it PROPOSES
Type "this job looks unhealthy — take a savepoint and stop it for me"
Sleep 1s
Enter

# → the model proposes stop_with_savepoint, shown for review (nothing ran)
Sleep 45s
Screenshot vhs/ai-propose.png
Sleep 1s

# press the proposal's number to open the human confirm dialog
Type "1"
Sleep 2s
Screenshot vhs/ai-propose-confirm.png
Sleep 2s

# decline — leave the job running
Escape
Sleep 1s
Escape
Sleep 1s
Escape
Sleep 1s
Type "q"
Sleep 1s
