// Package ports reserves local TCP ports for services.
package ports

import (
	"fmt"
	"net"
	"sync"
)

// Available reports whether addr accepts a new listener.
func Available(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// Pool hands out ports without giving the same one twice in a process.
type Pool struct {
	mu   sync.Mutex
	used map[int]struct{}
}

// Take returns preferred when it is free, then the next nearby ports, then an ephemeral port.
func (p *Pool) Take(preferred int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.used == nil {
		p.used = map[int]struct{}{}
	}
	if preferred > 0 {
		for port := preferred; port < preferred+30 && port <= 65535; port++ {
			if p.reserve(port) {
				return port, nil
			}
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if !p.reserve(port) {
		return 0, fmt.Errorf("port %d already reserved", port)
	}
	return port, nil
}

func (p *Pool) reserve(port int) bool {
	if _, ok := p.used[port]; ok {
		return false
	}
	if !Available(port) {
		return false
	}
	p.used[port] = struct{}{}
	return true
}
