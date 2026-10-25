package supervisor

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"lnch.sh/internal/certs"
	"lnch.sh/internal/proxy"
	"lnch.sh/internal/share"
	"lnch.sh/internal/tunnel"
)

func (s *Supervisor) ensureHTTPS(name string) {
	if !s.plan.HTTPS {
		return
	}
	s.mu.Lock()
	r := s.procs[name]
	if r == nil || r.httpsSrv != nil || r.port == 0 || r.spec.Protocol == "tcp" {
		s.mu.Unlock()
		return
	}
	port := r.port
	s.mu.Unlock()

	cert, err := s.certificate()
	if err != nil {
		s.addLog(name, "https: "+err.Error())
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.addLog(name, "https: "+err.Error())
		return
	}
	srv := &http.Server{
		Handler:           proxy.New("http://127.0.0.1:" + strconv.Itoa(port)),
		TLSNextProto:      map[string]func(*http.Server, *tls.Conn, http.Handler){},
		ReadHeaderTimeout: 10 * time.Second,
	}
	tlsLn := tls.NewListener(ln, &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
	})
	httpsPort := ln.Addr().(*net.TCPAddr).Port

	s.mu.Lock()
	r = s.procs[name]
	if r == nil || r.httpsSrv != nil || r.port != port {
		s.mu.Unlock()
		_ = ln.Close()
		return
	}
	r.httpsSrv = srv
	r.httpsPort = httpsPort
	r.httpsURL = "https://localhost:" + strconv.Itoa(httpsPort)
	s.mu.Unlock()
	s.persist()
	go func() { _ = srv.Serve(tlsLn) }()
}

func (s *Supervisor) certificate() (tls.Certificate, error) {
	return certs.Leaf()
}

// Share publishes the stack on the LAN and, when a relay is configured, a public URL.
func (s *Supervisor) Share(ctx context.Context) (string, error) {
	s.ensureShare(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shareURL == "" && s.shareErr != "" {
		return "", fmt.Errorf("%s", s.shareErr)
	}
	return s.shareURL, nil
}

func (s *Supervisor) ensureShare(ctx context.Context) {
	s.shareMu.Lock()
	defer s.shareMu.Unlock()
	if s.mux != nil {
		return
	}
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		s.setShare("", "", err.Error())
		return
	}
	mux := &share.Mux{}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	port := ln.Addr().(*net.TCPAddr).Port
	s.mu.Lock()
	s.mux = mux
	s.muxLn = ln
	s.mu.Unlock()
	go func() { _ = srv.Serve(ln) }()
	s.refreshMux()

	lan := fmt.Sprintf("http://127.0.0.1:%d/", port)
	hint := "No LAN address found. This URL is only on this machine."
	if ip := share.LanIPv4(); ip != "" {
		lan = fmt.Sprintf("http://%s:%d/", ip, port)
		hint = "Anyone on this network with the link can reach the dev server."
	}
	relay := s.plan.Share.Relay
	if relay == "" {
		if hint != "" && lan != "" {
			hint = "LAN only."
		}
		s.setShare(lan, hint, "")
		return
	}

	regCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := &tunnel.Client{Base: relay, RegisterToken: s.relayToken}
	reg, err := client.Open(regCtx, s.plan.Name)
	if err != nil {
		s.setShare(lan, "Relay unavailable. Sharing on the LAN instead.", err.Error())
		return
	}
	tctx, tcancel := context.WithCancel(s.rootCtx())
	s.mu.Lock()
	s.tunnelStop = tcancel
	s.mu.Unlock()
	go func() { _ = client.Serve(tctx, reg, fmt.Sprintf("http://127.0.0.1:%d", port)) }()
	url := reg.URL
	if url == "" {
		url = reg.DirectURL
	}
	s.setShare(url, "Anyone with the link can reach the dev server.", "")
}

func (s *Supervisor) setShare(url, hint, errText string) {
	s.mu.Lock()
	s.shareURL = url
	s.shareHint = hint
	s.shareErr = errText
	hook := s.hooks.OnShare
	s.mu.Unlock()
	s.persist()
	s.notify()
	if hook != nil {
		hook(url, hint, errText)
	}
}

func (s *Supervisor) refreshMux() {
	s.mu.Lock()
	names := make([]string, 0, len(s.procs))
	for name := range s.procs {
		names = append(names, name)
	}
	s.mu.Unlock()
	for _, name := range names {
		s.updateMux(name)
	}
}

func (s *Supervisor) updateMux(name string) {
	s.mu.Lock()
	r := s.procs[name]
	mux := s.mux
	if r == nil || mux == nil {
		s.mu.Unlock()
		return
	}
	target := ""
	if r.port > 0 && r.spec.Protocol != "tcp" {
		target = "http://127.0.0.1:" + strconv.Itoa(r.port)
	}
	expose := true
	if s.plan.AnyPublic() && !r.spec.Public && !r.spec.Primary && r.spec.Name != s.plan.Share.Service {
		expose = false
	}
	primary := r.spec.Primary || r.spec.Name == s.plan.PrimaryName()
	s.mu.Unlock()
	mux.Update(name, target, primary, expose && target != "")
}

func (s *Supervisor) closeExtra() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if s.tunnelStop != nil {
		s.tunnelStop()
	}
	if s.controlSrv != nil {
		_ = s.controlSrv.Shutdown(ctx)
	}
	if s.muxLn != nil {
		_ = s.muxLn.Close()
	}
	s.mu.Lock()
	var srvs []*http.Server
	for _, r := range s.procs {
		if r.httpsSrv != nil {
			srvs = append(srvs, r.httpsSrv)
		}
	}
	s.mu.Unlock()
	for _, srv := range srvs {
		_ = srv.Shutdown(ctx)
	}
}
