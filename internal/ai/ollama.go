package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/imankhadiv/f9s/internal/tools"
)

// ollamaNative speaks Ollama's own /api/chat dialect. Unlike the
// OpenAI-compat shim, tool-call arguments arrive as JSON objects (not
// strings) and the endpoint needs no auth — and models that mangle the shim
// often behave on the native API.
type ollamaNative struct {
	url   string
	model string
}

func (p *ollamaNative) Name() string { return "ollama/" + p.model }

// chat posts one non-streaming /api/chat turn.
func (p *ollamaNative) chat(ctx context.Context, payload map[string]any) (*ollamaMessage, error) {
	payload["model"] = p.model
	payload["stream"] = false
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(p.url, "/")+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: %s %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var out struct {
		Message ollamaMessage `json:"message"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("ollama: bad response: %w", err)
	}
	return &out.Message, nil
}

// ollamaMessage is /api/chat's message shape; tool-call arguments are
// objects, so they round-trip as raw JSON.
type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	Function struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

func (p *ollamaNative) Explain(ctx context.Context, evidence string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	msg, err := p.chat(ctx, map[string]any{
		"messages": []map[string]string{
			{"role": "system", "content": SystemPrompt},
			{"role": "user", "content": evidence},
		},
	})
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}

// Investigate runs the native-API tool loop.
func (p *ollamaNative) Investigate(ctx context.Context, seed string, reg *tools.Registry, scrub func(string) string, onStep StepFn, onPropose ProposeFn) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, agentWallClock)
	defer cancel()
	tr := newToolRunner(reg, scrub, onPropose)
	msgs := []any{
		map[string]string{"role": "system", "content": AgentSystemPrompt},
		map[string]string{"role": "user", "content": seed},
	}
	schemas := toolSchemas(reg)
	emptyResponses := 0
	for step := 0; step < maxAgentSteps; step++ {
		msg, err := p.chat(ctx, map[string]any{"messages": msgs, "tools": schemas})
		if err != nil {
			return "", err
		}
		if len(msg.ToolCalls) == 0 {
			if strings.TrimSpace(msg.Content) == "" {
				emptyResponses++
				if emptyResponses >= 2 {
					return "", fmt.Errorf("ollama returned empty responses twice — the model may not support tool use (qwen3:8b is the tested floor)")
				}
				msgs = append(msgs, map[string]string{"role": "user", "content": "your last response was empty — answer with the report or a tool call"})
				continue
			}
			return msg.Content, nil
		}
		msgs = append(msgs, msg) // echo the assistant turn verbatim
		for _, tc := range msg.ToolCalls {
			args := map[string]string{}
			for k, v := range tc.Function.Arguments {
				args[k] = fmt.Sprint(v)
			}
			onStep(describeCall(tc.Function.Name, args))
			result := tr.run(ctx, tc.Function.Name, args)
			msgs = append(msgs, map[string]string{
				"role": "tool", "tool_name": tc.Function.Name, "content": result,
			})
		}
	}
	return "", stepExhaustion(tr, maxAgentSteps)
}
