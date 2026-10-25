package tunnel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client opens tunnels on a relay.
type Client struct {
	Base          string
	RegisterToken string
	HTTP          *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Open registers a tunnel and returns its public URL.
func (c *Client) Open(ctx context.Context, name string) (Registration, error) {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Base, "/")+"/v1/tunnels", bytes.NewReader(body))
	if err != nil {
		return Registration{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.RegisterToken != "" {
		req.Header.Set("X-Lnch-Register", c.RegisterToken)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return Registration{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Registration{}, fmt.Errorf("relay register: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var reg Registration
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return Registration{}, err
	}
	if reg.URL == "" {
		reg.URL = reg.DirectURL
	}
	return reg, nil
}

// Serve forwards relay requests to target until ctx is cancelled.
func (c *Client) Serve(ctx context.Context, reg Registration, target string) error {
	local := &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req, ok, err := c.poll(ctx, reg)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(400 * time.Millisecond):
			}
			continue
		}
		if !ok {
			continue
		}
		go c.forward(ctx, local, reg, target, req)
	}
}

func (c *Client) poll(ctx context.Context, reg Registration) (wireReq, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Base, "/")+"/v1/tunnels/"+reg.ID+"/next", nil)
	if err != nil {
		return wireReq{}, false, err
	}
	req.Header.Set("Authorization", "Bearer "+reg.Token)
	resp, err := c.http().Do(req)
	if err != nil {
		return wireReq{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return wireReq{}, false, nil
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return wireReq{}, false, fmt.Errorf("relay poll: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var wr wireReq
	if err := json.NewDecoder(resp.Body).Decode(&wr); err != nil {
		return wireReq{}, false, err
	}
	return wr, true, nil
}

func (c *Client) forward(ctx context.Context, local *http.Client, reg Registration, target string, wr wireReq) {
	body, _ := base64.StdEncoding.DecodeString(wr.Body)
	path := wr.Path
	if path == "" {
		path = "/"
	}
	url := strings.TrimRight(target, "/") + path
	req, err := http.NewRequestWithContext(ctx, wr.Method, url, bytes.NewReader(body))
	if err != nil {
		_ = c.post(ctx, reg, wr.ID, http.StatusBadGateway, nil, []byte(err.Error()))
		return
	}
	for k, vals := range wr.Headers {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	resp, err := local.Do(req)
	if err != nil {
		_ = c.post(ctx, reg, wr.ID, http.StatusBadGateway, nil, []byte(err.Error()))
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		_ = c.post(ctx, reg, wr.ID, http.StatusBadGateway, nil, []byte(err.Error()))
		return
	}
	if len(respBody) > maxBody {
		_ = c.post(ctx, reg, wr.ID, http.StatusRequestEntityTooLarge, nil, []byte("response is too large"))
		return
	}
	_ = c.post(ctx, reg, wr.ID, resp.StatusCode, resp.Header, respBody)
}

func (c *Client) post(ctx context.Context, reg Registration, id string, status int, header http.Header, body []byte) error {
	payload := wireResp{
		Status:  status,
		Headers: filterHeaders(header),
		Body:    base64.StdEncoding.EncodeToString(body),
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Base, "/")+"/v1/tunnels/"+reg.ID+"/responses/"+id, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+reg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("relay response: %s", resp.Status)
	}
	return nil
}
