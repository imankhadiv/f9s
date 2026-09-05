// Package eval scores AI providers on frozen incident scenarios: replayable
// fixture clusters plus grading rules. It answers "is my local model good
// enough, or do I need a bigger one?" with data instead of vibes.
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/imankhadiv/f9s/internal/ai"
	"github.com/imankhadiv/f9s/internal/analyze"
	"github.com/imankhadiv/f9s/internal/client"
	"github.com/imankhadiv/f9s/internal/config"
	"github.com/imankhadiv/f9s/internal/tools"
)

// Scenario is one frozen incident: recorded API responses + grading rules.
type Scenario struct {
	Name string
	Dir  string
	Spec Spec
}

// Spec is scenario.yaml.
type Spec struct {
	JID          string     `yaml:"jid"`
	Description  string     `yaml:"description"`
	Expected     [][]string `yaml:"expected"`  // any group; all words of a group (case-insensitive)
	Forbidden    []string   `yaml:"forbidden"` // hallucination markers
	OptimalSteps int        `yaml:"optimalSteps"`
}

// Result is one provider × scenario grade.
type Result struct {
	Scenario   string  `json:"scenario"`
	Correct    bool    `json:"correct"`
	Halluc     bool    `json:"hallucinated"`
	Steps      int     `json:"steps"`
	Proposals  int     `json:"mutationProposals"`
	DurationS  float64 `json:"durationSec"`
	Err        string  `json:"error,omitempty"`
	Hypothesis string  `json:"hypothesis"`
}

// LoadScenarios reads every scenario under dir.
func LoadScenarios(dir string) ([]Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Scenario
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name(), "scenario.yaml"))
		if err != nil {
			continue
		}
		var spec Spec
		if err := yaml.Unmarshal(raw, &spec); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, Scenario{Name: e.Name(), Dir: filepath.Join(dir, e.Name()), Spec: spec})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no scenarios in %s", dir)
	}
	return out, nil
}

// fixtureServer serves the scenario's recorded responses; routes.json maps
// URL path → file.
func fixtureServer(dir string) (*httptest.Server, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "routes.json"))
	if err != nil {
		return nil, err
	}
	routes := map[string]string{}
	if err := json.Unmarshal(raw, &routes); err != nil {
		return nil, err
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			w.Write([]byte(`{"errors":["not in this frozen scenario"]}`))
			return
		}
		data, err := os.ReadFile(filepath.Join(dir, "fixtures", f))
		if err != nil {
			w.WriteHeader(500)
			return
		}
		w.Write(data)
	})), nil
}

// Run replays one scenario against an agent, scoring the outcome.
func Run(sc Scenario, agent ai.Agent) Result {
	res := Result{Scenario: sc.Name}
	srv, err := fixtureServer(sc.Dir)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	defer srv.Close()
	cl := client.New(config.Context{URL: srv.URL})
	seed, err := analyze.BuildEvidence(cl, sc.Spec.JID)
	if err != nil {
		res.Err = "evidence: " + err.Error()
		return res
	}
	reg := tools.New(cl, "eval")
	start := time.Now()
	steps := 0
	answer, err := agent.Investigate(context.Background(), seed, reg, func(s string) string { return s },
		func(string) { steps++ },
		func(ai.Proposal) { res.Proposals++ })
	res.DurationS = time.Since(start).Seconds()
	res.Steps = steps
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.Hypothesis = firstLine(answer)
	low := strings.ToLower(answer)
	for _, group := range sc.Spec.Expected {
		all := true
		for _, w := range group {
			if !strings.Contains(low, strings.ToLower(w)) {
				all = false
				break
			}
		}
		if all {
			res.Correct = true
			break
		}
	}
	for _, f := range sc.Spec.Forbidden {
		if strings.Contains(low, strings.ToLower(f)) {
			res.Halluc = true
			break
		}
	}
	return res
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 120 {
				l = l[:119] + "…"
			}
			return l
		}
	}
	return ""
}
