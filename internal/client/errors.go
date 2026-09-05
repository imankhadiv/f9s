package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrKind classifies a client failure so views can render an actionable
// hint instead of a raw status line.
type ErrKind int

const (
	KindNetwork ErrKind = iota
	KindTimeout
	KindAuth
	KindNotFound
	KindSchema
	KindServer
)

// ClientError is a classified REST failure with a user-actionable hint.
type ClientError struct {
	Kind ErrKind
	Op   string // "GET /jobs/..."
	Hint string
	Err  error
}

func (e *ClientError) Error() string {
	msg := e.Op
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	if e.Hint != "" {
		msg += " — " + e.Hint
	}
	return msg
}

func (e *ClientError) Unwrap() error { return e.Err }

// flinkErrors is Flink's structured error envelope.
type flinkErrors struct {
	Errors []string `json:"errors"`
}

// firstErrorLine extracts the first line of the first server-side error —
// the human part before the stack trace.
func firstErrorLine(body []byte) string {
	var fe flinkErrors
	if json.Unmarshal(body, &fe) != nil || len(fe.Errors) == 0 {
		return ""
	}
	line := strings.SplitN(fe.Errors[0], "\n", 2)[0]
	// strip the exception class prefix when a message follows it
	if i := strings.Index(line, "Exception: "); i >= 0 {
		line = line[i+len("Exception: "):]
	}
	return strings.TrimSpace(line)
}

// classifyStatus turns a non-2xx response into a ClientError.
func (c *Client) classifyStatus(op string, code int, body []byte) error {
	server := firstErrorLine(body)
	base := fmt.Errorf("HTTP %d", code)
	if server != "" {
		base = fmt.Errorf("HTTP %d: %s", code, server)
	}
	switch {
	case code == 401 || code == 403:
		return &ClientError{Kind: KindAuth, Op: op, Err: base,
			Hint: "the cluster rejected f9s's credentials — check this context's auth block (type/tokenEnv/user/passEnv) and that the env var is exported"}
	case code == 404:
		hint := "endpoint not found"
		if v := c.FlinkVersion(); v != "" {
			hint = "endpoint not found on Flink " + v + " — this feature may need a newer cluster"
		}
		return &ClientError{Kind: KindNotFound, Op: op, Err: base, Hint: hint}
	case code >= 500:
		return &ClientError{Kind: KindServer, Op: op, Err: base,
			Hint: "the JobManager errored — see :log for the call and the JM log (l) for the cause"}
	default:
		return &ClientError{Kind: KindServer, Op: op, Err: base}
	}
}

// classifyNetErr turns a transport failure into a ClientError.
func classifyNetErr(op string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &ClientError{Kind: KindTimeout, Op: op, Err: errors.New("timed out"),
			Hint: "the cluster is slow or unreachable — f9s backs off automatically; check the connection badge"}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return &ClientError{Kind: KindTimeout, Op: op, Err: errors.New("timed out"),
			Hint: "the cluster is slow or unreachable — f9s backs off automatically; check the connection badge"}
	}
	return &ClientError{Kind: KindNetwork, Op: op, Err: err,
		Hint: "cannot reach the cluster — verify the context url (and port-forward/VPN if remote)"}
}

// classifyDecode wraps a JSON decode failure as a schema mismatch.
func classifyDecode(op string, err error) error {
	return &ClientError{Kind: KindSchema, Op: op, Err: err,
		Hint: "unexpected response shape — a Flink version f9s hasn't been taught? please report this with the :log entry"}
}
