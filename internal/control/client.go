package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lnch.sh/internal/state"
)

// Client talks to a running supervisor.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// New builds a client from a state file.
func New(rt state.Runtime) *Client {
	return &Client{Base: strings.TrimRight(rt.Control, "/"), Token: rt.Token}
}

func (c *Client) http() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// Ping reports whether the control server answers.
func Ping(rt state.Runtime) bool {
	if rt.Control == "" || rt.Token == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	err := New(rt).get(ctx, "/v1/health", nil)
	return err == nil
}

// Runtime fetches the public snapshot.
func (c *Client) Runtime(ctx context.Context) (state.Runtime, error) {
	var rt state.Runtime
	if err := c.get(ctx, "/v1/status", &rt); err != nil {
		return state.Runtime{}, err
	}
	return rt, nil
}

// Dashboard fetches status and recent logs.
func (c *Client) Dashboard(ctx context.Context, tail int) (state.Runtime, map[string][]string, error) {
	var body struct {
		Runtime state.Runtime       `json:"runtime"`
		Logs    map[string][]string `json:"logs"`
	}
	if err := c.get(ctx, fmt.Sprintf("/v1/dashboard?tail=%d", tail), &body); err != nil {
		return state.Runtime{}, nil, err
	}
	return body.Runtime, body.Logs, nil
}

// Refresh matches the dashboard source used by the TUI.
func (c *Client) Refresh(ctx context.Context) (state.Runtime, map[string][]string, error) {
	return c.Dashboard(ctx, 400)
}

// Logs returns retained lines. An empty service returns every service.
func (c *Client) Logs(ctx context.Context, service string, tail int) (map[string][]string, error) {
	q := url.Values{}
	if service != "" {
		q.Set("service", service)
	}
	q.Set("tail", fmt.Sprintf("%d", tail))
	var body struct {
		Lines map[string][]string `json:"lines"`
	}
	if err := c.get(ctx, "/v1/logs?"+q.Encode(), &body); err != nil {
		return nil, err
	}
	return body.Lines, nil
}

// Restart asks the supervisor to restart one service.
func (c *Client) Restart(ctx context.Context, name string) error {
	return c.post(ctx, "/v1/services/"+url.PathEscape(name)+"/restart", nil)
}

// Share enables LAN or relay sharing.
func (c *Client) Share(ctx context.Context) (string, error) {
	var body struct {
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	if err := c.post(ctx, "/v1/share", &body); err != nil && body.URL == "" {
		return "", err
	}
	if body.URL == "" && body.Error != "" {
		return "", fmt.Errorf("%s", body.Error)
	}
	return body.URL, nil
}

// Shutdown stops the supervisor.
func (c *Client) Shutdown(ctx context.Context) error {
	return c.post(ctx, "/v1/shutdown", nil)
}

func (c *Client) get(ctx context.Context, path string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decode(resp, dest)
}

func (c *Client) post(ctx context.Context, path string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decode(resp, dest)
}

func decode(resp *http.Response, dest any) error {
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &body)
		if body.Error == "" {
			body.Error = strings.TrimSpace(string(b))
		}
		if body.Error == "" {
			body.Error = resp.Status
		}
		return fmt.Errorf("%s", body.Error)
	}
	if dest == nil || len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, dest)
}
