// Package ai is the provider abstraction behind explain-with-AI. Local-first:
// Ollama is the default provider; any OpenAI-compatible endpoint and the
// Anthropic API are equally supported. The LLM only ever sees the curated
// (and by default anonymized) evidence bundle — never raw cluster data.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/imankhadiv/f9s/internal/config"
)

const requestTimeout = 120 * time.Second

// SystemPrompt frames every explain request.
const SystemPrompt = `You are an Apache Flink SRE assistant embedded in a terminal UI.
You receive an evidence bundle collected from a Flink cluster's REST API.
Diagnose the most likely problem. Answer in this exact structure, in plain text (no markdown):
HYPOTHESIS: one or two sentences naming the most likely root cause.
EVIDENCE: 2-4 bullet lines, each citing a specific number or message from the bundle.
NEXT STEPS: 2-3 concrete suggestions. Suggest commands only as text; never assume they were run.
CONFIDENCE: high, medium, or low — with a clause explaining why.
Be concise. If the evidence shows a healthy job, say so plainly instead of inventing problems.`

// Provider answers one explain request.
type Provider interface {
	Explain(ctx context.Context, evidence string) (string, error)
	Name() string
}

// New builds the provider for a context's AI config.
func New(cfg config.AI) (Provider, error) {
	switch cfg.Provider {
	case "ollama":
		url := cfg.URL
		if url == "" {
			url = "http://localhost:11434"
		}
		model := cfg.Model
		if model == "" {
			return nil, fmt.Errorf("ai.model is required for the ollama provider")
		}
		return &ollamaNative{url: url, model: model}, nil
	case "openai":
		if cfg.URL == "" {
			return nil, fmt.Errorf("ai.url is required for the openai provider")
		}
		return &openAICompat{name: "openai", url: cfg.URL, model: cfg.Model, key: os.Getenv(cfg.TokenEnv)}, nil
	case "anthropic":
		key := os.Getenv(cfg.TokenEnv)
		if key == "" {
			key = os.Getenv("ANTHROPIC_API_KEY")
		}
		if key == "" {
			return nil, fmt.Errorf("anthropic provider needs an API key (ai.tokenEnv or ANTHROPIC_API_KEY)")
		}
		model := cfg.Model
		if model == "" {
			model = "claude-opus-5"
		}
		return &anthropicProvider{model: model, key: key}, nil
	case "":
		return nil, fmt.Errorf("no ai provider configured for this context")
	default:
		return nil, fmt.Errorf("unknown ai provider %q (want ollama, openai, or anthropic)", cfg.Provider)
	}
}

// openAICompat speaks the /v1/chat/completions dialect (Ollama, OpenAI, and
// every compatible gateway).
type openAICompat struct {
	name  string
	url   string
	model string
	key   string
}

func (p *openAICompat) Name() string { return p.name + "/" + p.model }

func (p *openAICompat) Explain(ctx context.Context, evidence string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":  p.model,
		"stream": false,
		"messages": []map[string]string{
			{"role": "system", "content": SystemPrompt},
			{"role": "user", "content": evidence},
		},
	})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("%s: %s %s", p.name, resp.Status, strings.TrimSpace(string(msg)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("%s returned no choices", p.name)
	}
	return out.Choices[0].Message.Content, nil
}

// anthropicProvider uses the official SDK.
type anthropicProvider struct {
	model string
	key   string
}

func (p *anthropicProvider) Name() string { return "anthropic/" + p.model }

func (p *anthropicProvider) Explain(ctx context.Context, evidence string) (string, error) {
	client := anthropic.NewClient(option.WithAPIKey(p.key))
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 2048,
		System:    []anthropic.TextBlockParam{{Text: SystemPrompt}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(evidence)),
		},
	})
	if err != nil {
		return "", err
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return "", fmt.Errorf("the model declined this request (%s)", resp.StopDetails.Category)
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String(), nil
}
