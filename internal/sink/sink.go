// Package sink delivers f9s alert events to outside systems — Slack,
// PagerDuty, a generic webhook, Prometheus Alertmanager — through one
// config-driven interface. Once alerts land where a team already watches,
// f9s stops being a thing one engineer runs and becomes infrastructure.
package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Event is the sink-facing view of an alert transition (a superset of what
// watchd emits, plus an optional AI diagnosis).
type Event struct {
	Type      string // alert_open | alert_close
	Kind      string
	Job       string
	Vertex    string
	Peak      string
	Context   string
	Diagnosis string // optional AI text (watch mode)
	Time      time.Time
}

// Opened reports whether this is a firing (not clearing) event.
func (e Event) Opened() bool { return e.Type == "alert_open" }

// Config declares one sink in config.yaml.
type Config struct {
	Type       string `yaml:"type"`       // slack | webhook | pagerduty | alertmanager
	URL        string `yaml:"url"`        // webhook URL (slack/webhook/alertmanager)
	RoutingKey string `yaml:"routingKey"` // pagerduty Events API v2 routing key
	MinKind    string `yaml:"minKind"`    // optional: only forward this alert kind
	OpenOnly   bool   `yaml:"openOnly"`   // skip alert_close events
}

// Sink delivers one event.
type Sink interface {
	Deliver(ctx context.Context, e Event) error
	Name() string
}

// Build constructs the sinks from config; unknown types are an error so a
// typo never silently drops alerts.
func Build(cfgs []Config) ([]Sink, error) {
	var out []Sink
	for _, c := range cfgs {
		var s Sink
		switch c.Type {
		case "slack":
			s = &httpSink{name: "slack", url: c.URL, body: slackBody}
		case "webhook":
			s = &httpSink{name: "webhook", url: c.URL, body: jsonBody}
		case "alertmanager":
			s = &httpSink{name: "alertmanager", url: c.URL, body: alertmanagerBody}
		case "pagerduty":
			s = &pagerdutySink{routingKey: c.RoutingKey}
		default:
			return nil, fmt.Errorf("unknown sink type %q (want slack, webhook, alertmanager, pagerduty)", c.Type)
		}
		out = append(out, &filtered{Sink: s, minKind: c.MinKind, openOnly: c.OpenOnly})
	}
	return out, nil
}

// filtered wraps a sink with per-sink event filtering.
type filtered struct {
	Sink
	minKind  string
	openOnly bool
}

func (f *filtered) Deliver(ctx context.Context, e Event) error {
	if f.openOnly && !e.Opened() {
		return nil
	}
	if f.minKind != "" && e.Kind != f.minKind {
		return nil
	}
	return f.Sink.Deliver(ctx, e)
}

// httpSink posts a body (built per-flavor) to a URL.
type httpSink struct {
	name string
	url  string
	body func(Event) ([]byte, string)
}

func (h *httpSink) Name() string { return h.name }

func (h *httpSink) Deliver(ctx context.Context, e Event) error {
	body, ctype := h.body(e)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ctype)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: HTTP %d", h.name, resp.StatusCode)
	}
	return nil
}

// summary is the shared human line.
func summary(e Event) string {
	verb := "FIRED"
	if !e.Opened() {
		verb = "cleared"
	}
	loc := e.Job
	if e.Vertex != "" {
		loc += " / " + e.Vertex
	}
	s := fmt.Sprintf("%s %s · %s", e.Kind, verb, loc)
	if e.Peak != "" && e.Opened() {
		s += " (peak " + e.Peak + ")"
	}
	return s
}

func slackBody(e Event) ([]byte, string) {
	emoji := ":rotating_light:"
	if !e.Opened() {
		emoji = ":white_check_mark:"
	}
	text := fmt.Sprintf("%s *f9s* [%s] %s", emoji, e.Context, summary(e))
	if e.Diagnosis != "" && e.Opened() {
		text += "\n> " + strings.ReplaceAll(strings.TrimSpace(e.Diagnosis), "\n", "\n> ")
	}
	b, _ := json.Marshal(map[string]string{"text": text})
	return b, "application/json"
}

func jsonBody(e Event) ([]byte, string) {
	b, _ := json.Marshal(map[string]any{
		"source": "f9s", "context": e.Context, "type": e.Type, "kind": e.Kind,
		"job": e.Job, "vertex": e.Vertex, "peak": e.Peak, "diagnosis": e.Diagnosis,
		"summary": summary(e), "time": e.Time, "schemaVersion": 1,
	})
	return b, "application/json"
}

func alertmanagerBody(e Event) ([]byte, string) {
	labels := map[string]string{
		"alertname": "f9s_" + e.Kind, "context": e.Context, "job": e.Job, "severity": "warning",
	}
	if e.Vertex != "" {
		labels["vertex"] = e.Vertex
	}
	alert := map[string]any{
		"labels":      labels,
		"annotations": map[string]string{"summary": summary(e), "diagnosis": e.Diagnosis},
		"startsAt":    e.Time.Format(time.RFC3339),
	}
	if !e.Opened() {
		alert["endsAt"] = e.Time.Format(time.RFC3339)
	}
	b, _ := json.Marshal([]any{alert})
	return b, "application/json"
}

// pagerdutySink triggers/resolves a PagerDuty Events API v2 incident.
type pagerdutySink struct{ routingKey string }

func (p *pagerdutySink) Name() string { return "pagerduty" }

func (p *pagerdutySink) Deliver(ctx context.Context, e Event) error {
	action := "trigger"
	if !e.Opened() {
		action = "resolve"
	}
	dedup := fmt.Sprintf("f9s-%s-%s-%s-%s", e.Context, e.Kind, e.Job, e.Vertex)
	payload := map[string]any{
		"routing_key":  p.routingKey,
		"event_action": action,
		"dedup_key":    dedup,
		"payload": map[string]any{
			"summary":        "f9s: " + summary(e),
			"source":         e.Context,
			"severity":       "warning",
			"component":      e.Job,
			"custom_details": map[string]string{"diagnosis": e.Diagnosis},
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://events.pagerduty.com/v2/enqueue", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("pagerduty: HTTP %d", resp.StatusCode)
	}
	return nil
}

// Dispatch delivers an event to every sink, collecting errors (one failing
// sink never blocks the others).
func Dispatch(ctx context.Context, sinks []Sink, e Event) []error {
	var errs []error
	for _, s := range sinks {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := s.Deliver(c, e); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
		}
		cancel()
	}
	return errs
}
