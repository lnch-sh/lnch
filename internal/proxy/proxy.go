// Package proxy forwards HTTP and WebSocket traffic to a local dev server.
package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// New returns a handler that proxies to target, including WebSocket upgrades.
func New(target string) http.Handler {
	u, err := url.Parse(target)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, err.Error(), http.StatusBadGateway)
		})
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.FlushInterval = 50 * time.Millisecond
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "lnch proxy: "+err.Error(), http.StatusBadGateway)
	}
	return &handler{target: u, rp: rp}
}

type handler struct {
	target *url.URL
	rp     *httputil.ReverseProxy
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		h.serveWS(w, r)
		return
	}
	h.rp.ServeHTTP(w, r)
}

func (h *handler) serveWS(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket upgrade is not supported", http.StatusInternalServerError)
		return
	}
	backend, err := net.Dial("tcp", h.target.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	client, bufrw, err := hj.Hijack()
	if err != nil {
		_ = backend.Close()
		return
	}
	out := r.Clone(r.Context())
	out.URL.Scheme = h.target.Scheme
	out.URL.Host = h.target.Host
	out.Host = h.target.Host
	if out.URL.Path == "" {
		out.URL.Path = "/"
	}
	out.RequestURI = out.URL.RequestURI()
	if err := out.Write(backend); err != nil {
		_ = backend.Close()
		_ = client.Close()
		return
	}
	go func() {
		_, _ = io.Copy(backend, bufrw)
		_ = backend.Close()
	}()
	_, _ = io.Copy(client, backend)
	_ = client.Close()
	_ = backend.Close()
}
