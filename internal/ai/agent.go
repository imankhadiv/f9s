package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/imankhadiv/f9s/internal/tools"
)

const (
	maxAgentSteps  = 10
	agentWallClock = 120 * time.Second
	maxResultBytes = 8 << 10
)

// AgentSystemPrompt frames the multi-step investigation.
const AgentSystemPrompt = `You are an Apache Flink SRE investigator embedded in a terminal UI.
You start from an evidence bundle and may call the provided tools to drill deeper
(fetch checkpoints, exceptions, taskmanagers, more evidence). Use at most a few targeted calls —
do not re-fetch what the bundle already shows, and never call the same tool with the same
arguments twice. When you have enough, answer in plain text:
HYPOTHESIS / EVIDENCE / NEXT STEPS / CONFIDENCE (same structure as before, concise).
Tools marked PROPOSAL ONLY queue an action for the human operator — call one only when a
mutation is clearly warranted, at most once, and keep investigating or conclude afterwards.
If the job is healthy, say so.`

// AskSystemPrompt frames free-form, multi-turn Q&A about the cluster. Unlike
// the investigation prompt it imposes no report template — it answers the
// operator's actual question — but keeps the same tool discipline and the
// same hard rule that mutations only ever get proposed.
const AskSystemPrompt = `You are an Apache Flink SRE assistant embedded in a terminal UI, answering an operator's questions about their live cluster.
You are given an evidence bundle (when a job is in focus) and the conversation so far. Answer the operator's latest question directly and concisely, in plain language — do NOT force a fixed report template.
You may call the provided read-only tools to fetch anything the evidence does not already contain (list jobs, get a job, checkpoints, exceptions, taskmanagers, more evidence). Prefer a few targeted calls and never repeat an identical call.
Ground every factual claim in the evidence or a tool result. If the data does not answer the question, say so plainly instead of guessing.
Tools marked PROPOSAL ONLY queue an action for the human operator — suggest one only when clearly warranted; nothing runs without their confirmation.`

// StepFn receives live progress lines ("→ get_checkpoints(jid=…)").
type StepFn func(line string)

// Proposal is a mutating action the model wants — never executed by the
// loop; the human confirms (or not) in the UI.
type Proposal struct {
	Tool string
	Args map[string]string
}

// ProposeFn receives action proposals.
type ProposeFn func(p Proposal)

// Agent is a provider that can run a multi-step tool-using loop, both for a
// one-shot investigation and for free-form conversational questions.
type Agent interface {
	Investigate(ctx context.Context, seed string, reg *tools.Registry, scrub func(string) string, onStep StepFn, onPropose ProposeFn) (string, error)
	// Ask answers a free-form question. The seed carries the grounding
	// evidence plus the conversation so far; continuity is the caller's
	// responsibility, so the loop itself stays stateless.
	Ask(ctx context.Context, seed string, reg *tools.Registry, scrub func(string) string, onStep StepFn, onPropose ProposeFn) (string, error)
}

// AsAgent returns the provider's agentic interface when it has one.
func AsAgent(p Provider) (Agent, bool) {
	a, ok := p.(Agent)
	return a, ok
}

// runTool executes a registry tool with loop-safety: read-only enforcement,
// duplicate-call breaking, and result truncation + scrubbing.
type toolRunner struct {
	reg       *tools.Registry
	scrub     func(string) string
	seen      map[string]bool
	dupes     int // duplicate-call attempts — thrash indicator
	onPropose ProposeFn
}

func newToolRunner(reg *tools.Registry, scrub func(string) string, onPropose ProposeFn) *toolRunner {
	return &toolRunner{reg: reg, scrub: scrub, seen: map[string]bool{}, onPropose: onPropose}
}

func (tr *toolRunner) run(ctx context.Context, name string, args map[string]string) string {
	fp := name + "|" + fmt.Sprint(args)
	if tr.seen[fp] {
		tr.dupes++
		return "you already ran this exact call — do not repeat it; conclude with what you have"
	}
	tr.seen[fp] = true
	t, ok := tr.reg.Get(name)
	if !ok {
		return "unknown tool: " + name
	}
	if t.Mutating {
		// The gate lives HERE, in Go — the model only ever proposes.
		if tr.onPropose != nil {
			tr.onPropose(Proposal{Tool: name, Args: args})
		}
		return "queued for operator approval — a human must confirm this action. Do NOT call it again or wait; finish your report and mention the proposal."
	}
	out, err := t.Run(ctx, args)
	if err != nil {
		return "tool error: " + err.Error()
	}
	if len(out) > maxResultBytes {
		cut := out[:maxResultBytes]
		// cut at a line boundary so the model never sees half a JSON value
		if i := strings.LastIndexByte(cut, '\n'); i > maxResultBytes/2 {
			cut = cut[:i]
		}
		out = cut + "\n…TRUNCATED at 8KB — the full result was larger; ask for a narrower slice instead of retrying the same call"
	}
	return tr.scrub(out)
}

func describeCall(name string, args map[string]string) string {
	parts := make([]string, 0, len(args))
	for k, v := range args {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	return "→ " + name + "(" + strings.Join(parts, ", ") + ")"
}

// ---- OpenAI-compatible (Ollama non-streaming included) ----

func (p *openAICompat) toolSchemas(reg *tools.Registry) []map[string]any {
	return toolSchemas(reg)
}

// toolSchemas renders the registry as OpenAI-style function schemas (the
// same shape Ollama's native API accepts).
func toolSchemas(reg *tools.Registry) []map[string]any {
	var out []map[string]any
	for _, t := range reg.List(true) {
		desc := t.Description
		if t.Mutating {
			desc = "[PROPOSAL ONLY — requires human approval; calling it queues the proposal] " + desc
		}
		t.Description = desc
		props := map[string]any{}
		var required []string
		for _, prm := range t.Params {
			props[prm.Name] = map[string]string{"type": "string", "description": prm.Description}
			if prm.Required {
				required = append(required, prm.Name)
			}
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": t.Name, "description": desc,
				"parameters": map[string]any{"type": "object", "properties": props, "required": required},
			},
		})
	}
	return out
}

// Investigate runs the function-calling loop against a chat-completions API.
// Streaming is intentionally off: several compat layers drop tool_calls when
// streaming.
func (p *openAICompat) Investigate(ctx context.Context, seed string, reg *tools.Registry, scrub func(string) string, onStep StepFn, onPropose ProposeFn) (string, error) {
	return p.runToolLoop(ctx, AgentSystemPrompt, seed, reg, scrub, onStep, onPropose)
}

// Ask runs the same tool loop with the conversational prompt.
func (p *openAICompat) Ask(ctx context.Context, seed string, reg *tools.Registry, scrub func(string) string, onStep StepFn, onPropose ProposeFn) (string, error) {
	return p.runToolLoop(ctx, AskSystemPrompt, seed, reg, scrub, onStep, onPropose)
}

func (p *openAICompat) runToolLoop(ctx context.Context, systemPrompt, seed string, reg *tools.Registry, scrub func(string) string, onStep StepFn, onPropose ProposeFn) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, agentWallClock)
	defer cancel()
	tr := newToolRunner(reg, scrub, onPropose)
	msgs := []map[string]any{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": seed},
	}
	schemas := p.toolSchemas(reg)
	emptyResponses := 0
	for step := 0; step < maxAgentSteps; step++ {
		body, err := json.Marshal(map[string]any{
			"model": p.model, "stream": false, "messages": msgs, "tools": schemas,
		})
		if err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			strings.TrimRight(p.url, "/")+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		if p.key != "" {
			req.Header.Set("Authorization", "Bearer "+p.key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		var out struct {
			Choices []struct {
				Message struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("%s: %s %s", p.name, resp.Status, strings.TrimSpace(string(data)))
		}
		if err := json.Unmarshal(data, &out); err != nil || len(out.Choices) == 0 {
			return "", fmt.Errorf("%s: bad response", p.name)
		}
		m := out.Choices[0].Message
		if len(m.ToolCalls) == 0 {
			if strings.TrimSpace(m.Content) == "" {
				emptyResponses++
				if emptyResponses >= 2 {
					return "", fmt.Errorf("the provider returned empty responses twice — model or endpoint looks degraded (check the model name and server logs)")
				}
				msgs = append(msgs, map[string]any{"role": "user", "content": "your last response was empty — answer with the report or a tool call"})
				continue
			}
			return m.Content, nil
		}
		// echo the assistant turn, then answer each call
		msgs = append(msgs, map[string]any{"role": "assistant", "content": m.Content, "tool_calls": rawCalls(data)})
		for _, tc := range m.ToolCalls {
			args := map[string]string{}
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			onStep(describeCall(tc.Function.Name, args))
			result := tr.run(ctx, tc.Function.Name, args)
			msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": tc.ID, "content": result})
		}
	}
	return "", stepExhaustion(tr, maxAgentSteps)
}

// stepExhaustion distinguishes thrash from genuine depth when the step
// budget runs out — the difference matters to whoever reads the error.
func stepExhaustion(tr *toolRunner, steps int) error {
	if tr.dupes >= 2 {
		return fmt.Errorf("investigation stopped after %d steps: the model kept repeating identical calls (%d duplicates) — likely too weak for tool use; try a larger model", steps, tr.dupes)
	}
	return fmt.Errorf("investigation used all %d steps without concluding — the problem may need manual digging; the audit trail shows everything it checked", steps)
}

// rawCalls re-extracts the raw tool_calls JSON so the echo back to the model
// is byte-faithful.
func rawCalls(data []byte) json.RawMessage {
	var probe struct {
		Choices []struct {
			Message struct {
				ToolCalls json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	_ = json.Unmarshal(data, &probe)
	if len(probe.Choices) > 0 {
		return probe.Choices[0].Message.ToolCalls
	}
	return json.RawMessage("[]")
}

// ---- Anthropic ----

// Investigate runs the tool-use loop via the official SDK.
func (p *anthropicProvider) Investigate(ctx context.Context, seed string, reg *tools.Registry, scrub func(string) string, onStep StepFn, onPropose ProposeFn) (string, error) {
	return p.runToolLoop(ctx, AgentSystemPrompt, seed, reg, scrub, onStep, onPropose)
}

// Ask runs the same tool loop with the conversational prompt.
func (p *anthropicProvider) Ask(ctx context.Context, seed string, reg *tools.Registry, scrub func(string) string, onStep StepFn, onPropose ProposeFn) (string, error) {
	return p.runToolLoop(ctx, AskSystemPrompt, seed, reg, scrub, onStep, onPropose)
}

func (p *anthropicProvider) runToolLoop(ctx context.Context, systemPrompt, seed string, reg *tools.Registry, scrub func(string) string, onStep StepFn, onPropose ProposeFn) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, agentWallClock)
	defer cancel()
	client := anthropic.NewClient(option.WithAPIKey(p.key))
	tr := newToolRunner(reg, scrub, onPropose)

	var toolParams []anthropic.ToolUnionParam
	for _, t := range reg.List(true) {
		if t.Mutating {
			t.Description = "[PROPOSAL ONLY — requires human approval; calling it queues the proposal] " + t.Description
		}
		props := map[string]any{}
		var required []string
		for _, prm := range t.Params {
			props[prm.Name] = map[string]string{"type": "string", "description": prm.Description}
			if prm.Required {
				required = append(required, prm.Name)
			}
		}
		toolParams = append(toolParams, anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.ToolInputSchemaParam{Properties: props, Required: required},
		}})
	}

	msgs := []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(seed))}
	for step := 0; step < maxAgentSteps; step++ {
		resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model(p.model),
			MaxTokens: 2048,
			System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
			Messages:  msgs,
			Tools:     toolParams,
		})
		if err != nil {
			return "", err
		}
		if resp.StopReason == anthropic.StopReasonRefusal {
			return "", fmt.Errorf("the model declined this request")
		}
		var text strings.Builder
		var results []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			switch b := block.AsAny().(type) {
			case anthropic.TextBlock:
				text.WriteString(b.Text)
			case anthropic.ToolUseBlock:
				args := map[string]string{}
				_ = json.Unmarshal(b.Input, &args)
				onStep(describeCall(b.Name, args))
				result := tr.run(ctx, b.Name, args)
				results = append(results, anthropic.NewToolResultBlock(b.ID, result, false))
			}
		}
		if len(results) == 0 {
			return text.String(), nil
		}
		msgs = append(msgs, resp.ToParam(), anthropic.NewUserMessage(results...))
	}
	return "", fmt.Errorf("investigation exceeded %d steps without concluding", maxAgentSteps)
}
