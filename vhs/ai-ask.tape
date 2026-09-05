# f9s AI "Ask" demo — render with: vhs vhs/ai-ask.tape
# Requires: the playground running (make playground / make chaos) AND an AI
# provider configured for the local context (ollama). See the AI section in README.
# Produces the GIF plus two stills used in the README: ai-ask-starters.png,
# ai-ask-answer.png.
Output vhs/ai-ask.gif

Set FontSize 15
Set Width 1200
Set Height 640
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms

Type "./f9s --config vhs/ai-demo-config.yaml"
Enter
Sleep 4s

# filter to the backpressured job, then open a chat scoped to it
Type "/"
Type "chaos_sink"
Enter
Sleep 1s
Type "@"
Sleep 2s

# a job-scoped empty chat offers job-specific starter questions
Screenshot vhs/ai-ask-starters.png
Sleep 1s

# ask a real diagnostic question — grounded in the job's evidence bundle
Type "why is this job backpressured, and what should I do?"
Sleep 1s
Enter

# the model reasons over the evidence (and can call read-only tools), then answers
Sleep 60s
Screenshot vhs/ai-ask-answer.png
Sleep 2s

Escape
Sleep 1s
Escape
Sleep 1s
Type "q"
Sleep 1s
