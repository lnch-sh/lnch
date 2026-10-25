package supervisor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"lnch.sh/internal/discover"
	"lnch.sh/internal/plan"
	"lnch.sh/internal/proc"
	"lnch.sh/internal/state"
)

func (s *Supervisor) startOne(ctx context.Context, spec plan.Service) error {
	s.mu.Lock()
	shutting := s.shutting
	s.mu.Unlock()
	if shutting {
		return fmt.Errorf("shutting down")
	}
	if spec.Dir == "" {
		spec.Dir = s.plan.Root
	}
	if st, err := os.Stat(spec.Dir); err != nil || !st.IsDir() {
		return fmt.Errorf("directory %s does not exist", spec.Dir)
	}

	known := s.portMap()
	assigned := 0
	if plan.NeedsPort(spec) {
		var err error
		assigned, err = s.pool.Take(spec.Port)
		if err != nil {
			return err
		}
	}
	command, warns := plan.Expand(spec.Command, assigned, known)
	extra := make(map[string]string, len(spec.Env))
	for k, v := range spec.Env {
		ev, ws := plan.Expand(v, assigned, known)
		extra[k] = ev
		warns = append(warns, ws...)
	}

	var cmd *exec.Cmd
	if len(spec.Argv) > 0 {
		cmd = exec.Command(spec.Argv[0], spec.Argv[1:]...)
	} else {
		cmd = proc.Shell(command)
	}
	cmd.Dir = spec.Dir
	cmd.Env = s.environ(extra, assigned, spec.Name)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	proc.ConfigureChild(cmd)
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return err
	}

	svcCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	r := s.procs[spec.Name]
	oldCancel := context.CancelFunc(nil)
	restarts := 0
	if r != nil {
		oldCancel = r.cancel
		restarts = r.restarts
	} else {
		r = &procState{}
		s.procs[spec.Name] = r
	}
	gen := r.gen + 1
	*r = procState{
		spec:     spec,
		status:   state.Starting,
		pid:      cmd.Process.Pid,
		port:     assigned,
		command:  command,
		gen:      gen,
		restarts: restarts,
		started:  time.Now(),
		cancel:   cancel,
	}
	if assigned > 0 && spec.Protocol != "tcp" {
		r.url = "http://127.0.0.1:" + strconv.Itoa(assigned)
	}
	if assigned > 0 && spec.Port > 0 && assigned != spec.Port {
		r.lastLine = fmt.Sprintf("port %d is busy; using %d", spec.Port, assigned)
	}
	snap := r.view()
	hook := s.hooks.OnStatus
	s.mu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}

	for _, w := range warns {
		s.addLog(spec.Name, "warning: "+w)
	}
	if snap.LastLine != "" {
		s.addLog(spec.Name, snap.LastLine)
	}
	s.notify()
	s.persist()
	if hook != nil {
		hook(snap)
	}
	s.updateMux(spec.Name)

	scanDone := make(chan struct{})
	go s.scan(spec.Name, pr, scanDone)
	go func() {
		err := cmd.Wait()
		_ = pw.Close()
		<-scanDone
		s.onExit(spec.Name, gen, err)
	}()
	go s.probe(svcCtx, spec.Name, gen)
	return nil
}

func (s *Supervisor) environ(extra map[string]string, port int, name string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra)+4)
	for _, e := range os.Environ() {
		if len(e) >= 5 && strings.EqualFold(e[:5], "PORT=") {
			continue
		}
		env = append(env, e)
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	if port > 0 {
		env = append(env, "PORT="+strconv.Itoa(port))
	}
	env = append(env, "LNCH=1", "LNCH_SERVICE="+name, "LNCH_ROOT="+s.plan.Root)
	return env
}

func (s *Supervisor) scan(name string, r io.Reader, done chan struct{}) {
	defer close(done)
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		line := discover.CleanLine(sc.Text())
		if strings.TrimSpace(line) == "" {
			continue
		}
		s.addLog(name, line)
		if p := discover.Port(line); p > 0 {
			s.notePort(name, p)
		}
	}
}

func (s *Supervisor) notePort(name string, port int) {
	if port <= 0 || port > 65535 {
		return
	}
	s.mu.Lock()
	r := s.procs[name]
	if r == nil || r.portLocked {
		s.mu.Unlock()
		return
	}
	if r.port == port {
		r.portLocked = true
		s.mu.Unlock()
		return
	}
	r.port = port
	r.portLocked = true
	if r.spec.Protocol != "tcp" {
		r.url = "http://127.0.0.1:" + strconv.Itoa(port)
	}
	snap := r.view()
	hook := s.hooks.OnStatus
	s.mu.Unlock()
	s.notify()
	s.persist()
	s.updateMux(name)
	if hook != nil {
		hook(snap)
	}
}

func (s *Supervisor) probe(ctx context.Context, name string, gen int) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	started := time.Now()
	s.mu.Lock()
	if r := s.procs[name]; r != nil && !r.started.IsZero() {
		started = r.started
	}
	s.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		r := s.procs[name]
		if r == nil || r.gen != gen || r.stopping || r.status == state.Exited {
			s.mu.Unlock()
			return
		}
		spec := r.spec
		port := r.port
		status := r.status
		s.mu.Unlock()

		if spec.Health == "none" {
			if time.Since(started) > 400*time.Millisecond {
				s.mark(name, gen, state.Healthy, "")
			}
			continue
		}
		timeout := spec.ReadyTimeout
		if timeout <= 0 {
			timeout = 45 * time.Second
		}
		if port > 0 {
			if checkHealth(ctx, spec.Health, port, s.portMap()) {
				s.ensureHTTPS(name)
				s.mark(name, gen, state.Healthy, "")
				return
			}
			if time.Since(started) > timeout {
				s.mark(name, gen, state.Unhealthy, "health check timed out")
				return
			}
			continue
		}
		wait := 5 * time.Second
		if s.hasDependents(name) {
			wait = timeout
		}
		if time.Since(started) > wait && status == state.Starting {
			s.mark(name, gen, state.Running, "")
		}
	}
}

func (s *Supervisor) hasDependents(name string) bool {
	for _, svc := range s.plan.Services {
		for _, d := range svc.DependsOn {
			if d == name {
				return true
			}
		}
	}
	return false
}

func (s *Supervisor) mark(name string, gen int, status, line string) {
	s.mu.Lock()
	r := s.procs[name]
	if r == nil || r.gen != gen || r.stopping || r.status == state.Exited {
		s.mu.Unlock()
		return
	}
	if r.status == status && line == "" {
		s.mu.Unlock()
		return
	}
	r.status = status
	if status == state.Healthy {
		r.portLocked = true
		if r.healthy.IsZero() {
			r.healthy = time.Now()
		}
	}
	if line != "" {
		r.lastLine = line
	}
	snap := r.view()
	hook := s.hooks.OnStatus
	s.mu.Unlock()
	if line != "" {
		s.addLog(name, line)
	}
	s.notify()
	s.persist()
	if hook != nil {
		hook(snap)
	}
}

func (s *Supervisor) onExit(name string, gen int, err error) {
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	s.mu.Lock()
	r := s.procs[name]
	if r == nil || r.gen != gen || r.stopping || s.shutting {
		s.mu.Unlock()
		return
	}
	r.status = state.Exited
	r.lastLine = fmt.Sprintf("exited (%d)", code)
	policy := r.spec.Restart
	restarts := r.restarts
	spec := r.spec
	snap := r.view()
	hook := s.hooks.OnStatus
	s.mu.Unlock()
	s.addLog(name, snap.LastLine)
	s.notify()
	s.persist()
	if hook != nil {
		hook(snap)
	}
	if policy != "on-failure" || code == 0 || restarts >= 5 {
		return
	}
	s.mu.Lock()
	if cur := s.procs[name]; cur != nil && cur.gen == gen {
		cur.restarts++
	}
	s.mu.Unlock()
	delay := time.Duration(300*(1<<min(restarts, 3))) * time.Millisecond
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-s.rootCtx().Done():
			return
		case <-timer.C:
		}
		_ = s.startOne(s.rootCtx(), spec)
	}()
}

// Restart stops a service and starts it again.
func (s *Supervisor) Restart(ctx context.Context, name string) error {
	_ = ctx
	s.mu.Lock()
	r := s.procs[name]
	if r == nil {
		s.mu.Unlock()
		known := make([]string, 0, len(s.plan.Services))
		for _, svc := range s.plan.Services {
			known = append(known, svc.Name)
		}
		example := "web"
		if len(known) > 0 {
			example = known[0]
		}
		return fmt.Errorf("unknown service %q\nServices: %s\nExample: lnch restart %s", name, strings.Join(known, ", "), example)
	}
	r.stopping = true
	r.restarts = 0
	pid := r.pid
	cancel := r.cancel
	spec := r.spec
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if pid > 0 {
		_ = killPID(pid)
	}
	s.markRestart(name)
	return s.startOne(s.rootCtx(), spec)
}

func (s *Supervisor) markRestart(name string) {
	s.mu.Lock()
	r := s.procs[name]
	if r == nil {
		s.mu.Unlock()
		return
	}
	r.status = state.Restarting
	r.lastLine = "restarting"
	snap := r.view()
	hook := s.hooks.OnStatus
	s.mu.Unlock()
	s.notify()
	s.persist()
	if hook != nil {
		hook(snap)
	}
}

func checkHealth(ctx context.Context, health string, port int, ports map[string]int) bool {
	h, _ := plan.Expand(health, port, ports)
	if strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://") {
		reqCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, h, nil)
		if err != nil {
			return false
		}
		resp, err := probeClient.Do(req)
		if err != nil {
			return false
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode > 0 && resp.StatusCode < 500
	}
	addr := strings.TrimPrefix(h, "tcp://")
	if addr == "" || addr == "none" {
		addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	}
	d := net.Dialer{Timeout: 400 * time.Millisecond}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

var probeClient = &http.Client{Timeout: time.Second}

func killPID(pid int) error {
	if pid <= 0 {
		return nil
	}
	return proc.KillTree(pid)
}
