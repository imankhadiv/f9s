// Package runbook loads user-authored playbooks and matches them to
// incidents, so investigations follow house rules instead of LLM vibes.
package runbook

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Runbook is one playbook: matchers from frontmatter plus the body the
// agent must follow.
type Runbook struct {
	Name      string
	JobGlob   string // glob on the job name; empty matches all
	Exception string // substring (case-insensitive) on evidence text; empty matches all
	Kind      string // alert kind (backpressure/skew/...); empty matches all
	Body      string
}

// Dir is ~/.config/f9s/runbooks (XDG-aware).
func Dir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "f9s", "runbooks")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "runbooks"
	}
	return filepath.Join(home, ".config", "f9s", "runbooks")
}

var frontRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n?`)

// Load reads every *.md runbook in dir. A file without frontmatter matches
// everything.
func Load(dir string) []Runbook {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Runbook
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		rb := Runbook{Name: strings.TrimSuffix(e.Name(), ".md"), Body: string(data)}
		if m := frontRe.FindStringSubmatch(rb.Body); m != nil {
			rb.Body = strings.TrimPrefix(rb.Body, m[0])
			for _, line := range strings.Split(m[1], "\n") {
				k, v, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				v = strings.Trim(strings.TrimSpace(v), `"'`)
				switch strings.TrimSpace(k) {
				case "job":
					rb.JobGlob = v
				case "exception":
					rb.Exception = v
				case "kind":
					rb.Kind = v
				}
			}
		}
		rb.Body = strings.TrimSpace(rb.Body)
		if rb.Body != "" {
			out = append(out, rb)
		}
	}
	return out
}

// Match returns the runbooks applying to this incident.
func Match(books []Runbook, job, evidence, kind string) []Runbook {
	var out []Runbook
	for _, rb := range books {
		if rb.JobGlob != "" {
			if ok, _ := filepath.Match(rb.JobGlob, job); !ok && !strings.Contains(job, strings.Trim(rb.JobGlob, "*")) {
				continue
			}
		}
		if rb.Exception != "" && !strings.Contains(strings.ToLower(evidence), strings.ToLower(rb.Exception)) {
			continue
		}
		if rb.Kind != "" && kind != "" && rb.Kind != kind {
			continue
		}
		out = append(out, rb)
	}
	return out
}

// Inject prepends matched runbooks to an investigation seed as
// authoritative guidance.
func Inject(seed, job, kind string) string {
	books := Match(Load(Dir()), job, seed, kind)
	if len(books) == 0 {
		return seed
	}
	var b strings.Builder
	for _, rb := range books {
		b.WriteString("TEAM RUNBOOK \"" + rb.Name + "\" (authoritative — follow these steps before free reasoning):\n")
		b.WriteString(rb.Body + "\n\n")
	}
	return b.String() + seed
}
