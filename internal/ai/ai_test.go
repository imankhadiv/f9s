package ai

import (
	"strings"
	"testing"

	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/tools"
)

func TestNewProviderSelection(t *testing.T) {
	if _, err := New(config.AI{}); err == nil {
		t.Error("empty provider should error")
	}
	if _, err := New(config.AI{Provider: "wat"}); err == nil {
		t.Error("unknown provider should error")
	}
	if _, err := New(config.AI{Provider: "ollama"}); err == nil {
		t.Error("ollama without a model should error")
	}
	p, err := New(config.AI{Provider: "ollama", Model: "qwen3:8b"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Name(), "qwen3:8b") {
		t.Errorf("provider name lost the model: %q", p.Name())
	}
	if _, err := New(config.AI{Provider: "openai", Model: "gpt"}); err == nil {
		t.Error("openai without a url should error")
	}
	// anthropic needs a key from the environment.
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := New(config.AI{Provider: "anthropic"}); err == nil {
		t.Error("anthropic without a key should error")
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	if p, err := New(config.AI{Provider: "anthropic"}); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(p.Name(), "claude") {
		t.Errorf("anthropic default model not applied: %q", p.Name())
	}
}

func TestDescribeCall(t *testing.T) {
	got := describeCall("get_job", map[string]string{"jid": "abc", "empty": ""})
	if !strings.Contains(got, "get_job") || !strings.Contains(got, "jid=abc") {
		t.Errorf("describeCall = %q", got)
	}
	if strings.Contains(got, "empty=") {
		t.Error("describeCall should drop empty args")
	}
}

func TestToolSchemasFlagMutating(t *testing.T) {
	reg := tools.New(nil, "prod")
	schemas := toolSchemas(reg)
	if len(schemas) == 0 {
		t.Fatal("no schemas rendered")
	}
	var sawProposalGuard bool
	for _, s := range schemas {
		fn := s["function"].(map[string]any)
		if strings.Contains(fn["description"].(string), "PROPOSAL ONLY") {
			sawProposalGuard = true
		}
		// every schema must carry an object parameter block
		if _, ok := fn["parameters"].(map[string]any)["properties"]; !ok {
			t.Errorf("schema %v missing properties", fn["name"])
		}
	}
	if !sawProposalGuard {
		t.Error("mutating tools must be marked PROPOSAL ONLY in their schema — the human-approval guard is untested")
	}
}
