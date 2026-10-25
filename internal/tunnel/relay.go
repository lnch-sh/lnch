// Package tunnel is a small outbound relay for public dev URLs.
package tunnel

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxBody = 16 << 20

// PublicRelay is the hosted tunnel at lnch.sh.
const PublicRelay = "https://lnch.sh"

const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// Relay accepts public HTTP and hands each request to the client that opened the tunnel.
type Relay struct {
	Domain        string
	Scheme        string
	RegisterToken string

	mu      sync.Mutex
	tunnels map[string]*tunnel
	addr    string
}

type tunnel struct {
	id      string
	token   string
	queue   chan *job
	pending map[string]*job
}

type job struct {
	id      string
	method  string
	path    string
	headers map[string][]string
	body    []byte
	done    chan result
}

type result struct {
	status  int
	headers map[string][]string
	body    []byte
}

type wireReq struct {
	ID      string              `json:"id"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

type wireResp struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

// Registration is what a client receives after opening a tunnel.
type Registration struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	DirectURL string `json:"direct_url"`
	Token     string `json:"token"`
}

// SetAddr records the address the relay is listening on.
func (rl *Relay) SetAddr(addr string) {
	rl.mu.Lock()
	rl.addr = addr
	rl.mu.Unlock()
}

// ServeHTTP is the relay's public and control API.
func (rl *Relay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		rl.api(w, r)
		return
	}
	rl.public(w, r)
}

func (rl *Relay) api(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/tunnels":
		rl.register(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/tunnels/") && strings.HasSuffix(r.URL.Path, "/next"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/tunnels/"), "/next")
		rl.next(w, r, id)
	case r.Method == http.MethodPost && strings.Count(r.URL.Path, "/") == 5 && strings.Contains(r.URL.Path, "/responses/"):
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		// v1 tunnels {id} responses {req}
		if len(parts) != 5 {
			http.NotFound(w, r)
			return
		}
		rl.respond(w, r, parts[2], parts[4])
	default:
		http.NotFound(w, r)
	}
}

func (rl *Relay) register(w http.ResponseWriter, r *http.Request) {
	if rl.RegisterToken != "" && !fixed(r.Header.Get("X-Lnch-Register"), rl.RegisterToken) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	t := &tunnel{
		id:      uniqueID(8),
		token:   randomHex(24),
		queue:   make(chan *job, 32),
		pending: map[string]*job{},
	}
	rl.mu.Lock()
	if rl.tunnels == nil {
		rl.tunnels = map[string]*tunnel{}
	}
	for rl.tunnels[t.id] != nil {
		t.id = uniqueID(8)
	}
	rl.tunnels[t.id] = t
	addr := rl.addr
	rl.mu.Unlock()

	host := directHost(addr)
	direct := "http://" + host + "/t/" + t.id + "/"
	pretty := direct
	scheme := rl.Scheme
	if scheme == "" {
		scheme = "http"
	}
	if rl.Domain != "" {
		pretty = scheme + "://" + t.id + "." + rl.Domain + "/"
	}
	writeJSON(w, http.StatusCreated, Registration{ID: t.id, URL: pretty, DirectURL: direct, Token: t.token})
}

func (rl *Relay) next(w http.ResponseWriter, r *http.Request, id string) {
	t := rl.get(id)
	if t == nil || !bearer(r, t.token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	select {
	case job := <-t.queue:
		rl.mu.Lock()
		t.pending[job.id] = job
		rl.mu.Unlock()
		writeJSON(w, http.StatusOK, wireReq{
			ID: job.id, Method: job.method, Path: job.path, Headers: job.headers,
			Body: base64.StdEncoding.EncodeToString(job.body),
		})
	case <-time.After(20 * time.Second):
		w.WriteHeader(http.StatusNoContent)
	case <-r.Context().Done():
	}
}

func (rl *Relay) respond(w http.ResponseWriter, r *http.Request, id, reqID string) {
	t := rl.get(id)
	if t == nil || !bearer(r, t.token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var wire wireResp
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody+1024)).Decode(&wire); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, err := base64.StdEncoding.DecodeString(wire.Body)
	if err != nil {
		http.Error(w, "body is not base64", http.StatusBadRequest)
		return
	}
	rl.mu.Lock()
	job := t.pending[reqID]
	delete(t.pending, reqID)
	rl.mu.Unlock()
	if job == nil {
		http.Error(w, "unknown request", http.StatusNotFound)
		return
	}
	select {
	case job.done <- result{status: wire.Status, headers: wire.Headers, body: body}:
	default:
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (rl *Relay) public(w http.ResponseWriter, r *http.Request) {
	id, path := rl.route(r)
	t := rl.get(id)
	if t == nil {
		http.Error(w, "unknown tunnel", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) > maxBody {
		http.Error(w, "request body is too large", http.StatusRequestEntityTooLarge)
		return
	}
	if path == "" {
		path = "/"
	}
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	job := &job{
		id:      randomHex(8),
		method:  r.Method,
		path:    path,
		headers: filterHeaders(r.Header),
		body:    body,
		done:    make(chan result, 1),
	}
	select {
	case t.queue <- job:
	case <-r.Context().Done():
		return
	default:
		http.Error(w, "tunnel is busy", http.StatusServiceUnavailable)
		return
	}
	select {
	case res := <-job.done:
		for k, vals := range res.headers {
			if hop(k) {
				continue
			}
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		if res.status == 0 {
			res.status = http.StatusOK
		}
		w.WriteHeader(res.status)
		_, _ = w.Write(res.body)
	case <-time.After(60 * time.Second):
		http.Error(w, "tunnel timed out", http.StatusGatewayTimeout)
	case <-r.Context().Done():
	}
}

func (rl *Relay) route(r *http.Request) (id, path string) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	if rl.Domain != "" && host != rl.Domain && strings.HasSuffix(host, "."+strings.ToLower(rl.Domain)) {
		id = strings.TrimSuffix(host, "."+strings.ToLower(rl.Domain))
		if id != "" && !strings.Contains(id, ".") {
			return id, r.URL.Path
		}
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, "/t/"); ok {
		name, p, _ := strings.Cut(rest, "/")
		if p == "" {
			p = "/"
		} else {
			p = "/" + p
		}
		return name, p
	}
	return "", ""
}

func (rl *Relay) get(id string) *tunnel {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.tunnels == nil {
		return nil
	}
	return rl.tunnels[id]
}

func directHost(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		if addr == "" {
			return "127.0.0.1"
		}
		return addr
	}
	return net.JoinHostPort("127.0.0.1", port)
}

func filterHeaders(h http.Header) map[string][]string {
	out := map[string][]string{}
	for k, v := range h {
		if hop(k) || strings.EqualFold(k, "Host") {
			continue
		}
		out[k] = append([]string(nil), v...)
	}
	return out
}

func hop(k string) bool {
	switch strings.ToLower(k) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade", "content-length":
		return true
	default:
		return false
	}
}

func bearer(r *http.Request, token string) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return fixed(got, token)
}

func fixed(got, want string) bool {
	if len(got) != len(want) || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func uniqueID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	out := make([]byte, n)
	for i := range b {
		out[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(out)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	const hexdigits = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out)
}
