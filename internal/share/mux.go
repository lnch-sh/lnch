// Package share exposes local services on the LAN and through a relay.
package share

import (
	"net"
	"net/http"
	"strings"
	"sync"

	"lnch.sh/internal/proxy"
)

// Mux routes / to the primary service and /_lnch/{name}/ to the others.
type Mux struct {
	mu      sync.RWMutex
	routes  map[string]string
	primary string
}

// Update records where a service is listening. An empty target removes it.
func (m *Mux) Update(name, target string, primary, expose bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.routes == nil {
		m.routes = map[string]string{}
	}
	if !expose || target == "" {
		delete(m.routes, name)
		if m.primary == name {
			m.primary = ""
		}
		return
	}
	m.routes[name] = target
	if primary {
		m.primary = name
	}
}

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/_lnch/") {
		rest := strings.TrimPrefix(r.URL.Path, "/_lnch/")
		name, path, _ := strings.Cut(rest, "/")
		path = "/" + path
		m.mu.RLock()
		target := m.routes[name]
		m.mu.RUnlock()
		if target == "" {
			http.NotFound(w, r)
			return
		}
		r.URL.Path = path
		r.RequestURI = ""
		proxy.New(target).ServeHTTP(w, r)
		return
	}
	m.mu.RLock()
	name := m.primary
	target := ""
	if name != "" {
		target = m.routes[name]
	}
	if target == "" {
		for n, u := range m.routes {
			name = n
			target = u
			break
		}
	}
	m.mu.RUnlock()
	if target == "" {
		http.Error(w, "lnch: the primary service is not listening yet", http.StatusServiceUnavailable)
		return
	}
	proxy.New(target).ServeHTTP(w, r)
}

// LanIPv4 returns the first private-looking IPv4 address.
func LanIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil || ipnet.IP.IsLinkLocalUnicast() {
				continue
			}
			return ipnet.IP.String()
		}
	}
	return ""
}
