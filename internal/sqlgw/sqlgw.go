// Package sqlgw is a minimal client for the Flink SQL Gateway REST API (v2),
// which runs separately from the JobManager (default port 8083).
package sqlgw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to one SQL Gateway.
type Client struct {
	base string
	hc   *http.Client
}

// New builds a gateway client.
func New(url string) *Client {
	return &Client{base: strings.TrimRight(url, "/"), hc: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) call(method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("%s %s: %s %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// OpenSession creates a gateway session and returns its handle.
func (c *Client) OpenSession() (string, error) {
	var v struct {
		SessionHandle string `json:"sessionHandle"`
	}
	err := c.call(http.MethodPost, "/v2/sessions", map[string]any{"properties": map[string]string{}}, &v)
	return v.SessionHandle, err
}

// Execute submits a statement and returns the operation handle.
func (c *Client) Execute(session, statement string) (string, error) {
	var v struct {
		OperationHandle string `json:"operationHandle"`
	}
	err := c.call(http.MethodPost, "/v2/sessions/"+session+"/statements",
		map[string]any{"statement": statement}, &v)
	return v.OperationHandle, err
}

type resultPage struct {
	ResultType string `json:"resultType"` // NOT_READY / PAYLOAD / EOS
	Results    struct {
		Columns []struct {
			Name string `json:"name"`
		} `json:"columns"`
		Data []struct {
			Kind   string `json:"kind"`
			Fields []any  `json:"fields"`
		} `json:"data"`
	} `json:"results"`
	NextResultURI string `json:"nextResultUri"`
}

// FetchAll polls an operation's results until EOS, maxRows, or the deadline —
// streaming SELECTs never end on their own, so the row cap is the exit. The
// operation is closed before returning.
func (c *Client) FetchAll(session, op string, maxRows int, deadline time.Duration) ([]string, [][]string, error) {
	defer func() { _ = c.call(http.MethodDelete, "/v2/sessions/"+session+"/operations/"+op+"/close", nil, nil) }()
	var cols []string
	var rows [][]string
	path := "/v2/sessions/" + session + "/operations/" + op + "/result/0?rowFormat=JSON"
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		var page resultPage
		if err := c.call(http.MethodGet, path, nil, &page); err != nil {
			return cols, rows, err
		}
		if page.ResultType == "NOT_READY" {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if len(cols) == 0 {
			for _, col := range page.Results.Columns {
				cols = append(cols, col.Name)
			}
		}
		for _, d := range page.Results.Data {
			row := make([]string, len(d.Fields))
			for i, f := range d.Fields {
				row[i] = fmt.Sprint(f)
			}
			if d.Kind != "" && d.Kind != "INSERT" {
				row[0] = d.Kind + " " + row[0]
			}
			rows = append(rows, row)
			if len(rows) >= maxRows {
				return cols, rows, nil
			}
		}
		if page.ResultType == "EOS" || page.NextResultURI == "" {
			return cols, rows, nil
		}
		path = strings.TrimPrefix(page.NextResultURI, c.baseURLPathPrefix())
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		time.Sleep(150 * time.Millisecond)
	}
	return cols, rows, nil
}

// baseURLPathPrefix strips a scheme://host prefix if the gateway returns
// absolute next-result URIs (it normally returns paths like /v2/...).
func (c *Client) baseURLPathPrefix() string { return c.base }
