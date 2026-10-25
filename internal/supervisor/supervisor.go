// Package supervisor starts a project plan, watches health, and serves the control API.
package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"lnch.sh/internal/control"
	"lnch.sh/internal/plan"
	"lnch.sh/internal/ports"
	"lnch.sh/internal/share"
	"lnch.sh/internal/state"
)

// Hooks receive log and status changes. They must not block for long.
type Hooks struct {
	OnLog    func(service, line string)
	OnStatus func(service state.Service)
	OnShare  func(url, hint, err string)
}

// Options configures a supervisor.
type Options struct {
	Plan       plan.Plan
	Share      bool
	RelayToken string
	LogFile    string
	Hooks      Hooks
}

// Supervisor owns the processes for one project.
type Supervisor struct {
	plan       plan.Plan
	shareAtRun bool
	relayToken string
	logFile    string
	hooks      Hooks

	mu         sync.Mutex
	procs      map[string]*procState
	token      string
	controlURL string
	shareURL   string
	shareHint  string
	shareErr   string
	started    time.Time
	shutting   bool
	changed    chan struct{}
	generation uint64
	cancel     context.CancelFunc
	run        context.Context

	logs      *logBuf
	pool      *ports.Pool
	ready     chan struct{}
	done      chan struct{}
	readyOnce sync.Once
	stopOnce  sync.Once

	controlSrv *http.Server
	mux        *share.Mux
	muxLn      net.Listener
	shareMu    sync.Mutex
	tunnelStop context.CancelFunc
}

type procState struct {
	spec       plan.Service
	status     string
	pid        int
	port       int
	portLocked bool
	httpsPort  int
	url        string
	httpsURL   string
	command    string
	lastLine   string
	gen        int
	stopping   bool
	restarts   int
	started    time.Time
	healthy    time.Time
	httpsSrv   *http.Server
	cancel     context.CancelFunc
}

// Start validates a plan and returns a supervisor. Call Run to launch it.
func Start(opt Options) (*Supervisor, error) {
	p := opt.Plan
	if p.Root == "" {
		return nil, fmt.Errorf("plan is missing a root")
	}
	for i := range p.Services {
		if p.Services[i].Dir == "" {
			p.Services[i].Dir = p.Root
		}
		if p.Services[i].Name == p.PrimaryName() {
			p.Services[i].Primary = true
		}
		if p.Services[i].Protocol == "" {
			p.Services[i].Protocol = "http"
		}
	}
	if err := plan.Validate(&p); err != nil {
		return nil, err
	}
	return &Supervisor{
		plan:       p,
		shareAtRun: opt.Share,
		relayToken: opt.RelayToken,
		logFile:    opt.LogFile,
		hooks:      opt.Hooks,
		procs:      map[string]*procState{},
		token:      newToken(),
		started:    time.Now(),
		changed:    make(chan struct{}),
		logs:       &logBuf{},
		pool:       &ports.Pool{},
		ready:      make(chan struct{}),
		done:       make(chan struct{}),
	}, nil
}

// Ready closes when the control API is listening.
func (s *Supervisor) Ready() <-chan struct{} { return s.ready }

// Run launches services and blocks until the context is cancelled.
func (s *Supervisor) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.run = ctx
	s.mu.Unlock()
	defer func() {
		cancel()
		s.stopAll()
		s.closeExtra()
		_ = state.Remove(s.plan.Root)
		close(s.done)
	}()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: control.Handler(s.token, s), ReadHeaderTimeout: 5 * time.Second}
	s.controlSrv = srv
	go func() { _ = srv.Serve(ln) }()
	s.mu.Lock()
	s.controlURL = "http://" + ln.Addr().String()
	s.mu.Unlock()
	s.persist()
	s.readyOnce.Do(func() { close(s.ready) })

	if s.shareAtRun {
		go s.ensureShare(ctx)
	}
	go s.loop(ctx)
	<-ctx.Done()
	return nil
}

// Stop cancels Run without waiting. The HTTP shutdown handler uses this.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Shutdown cancels Run and waits for processes to stop.
func (s *Supervisor) Shutdown(ctx context.Context) error {
	s.Stop()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		s.stopAll()
		return ctx.Err()
	case <-time.After(8 * time.Second):
		s.stopAll()
		return nil
	}
}

// Runtime is the public snapshot. The control token is omitted.
func (s *Supervisor) Runtime() state.Runtime {
	rt := s.snapshot()
	rt.Token = ""
	return rt
}

// Logs returns retained lines. An empty service name returns every service.
func (s *Supervisor) Logs(service string, tail int) map[string][]string {
	if tail <= 0 {
		tail = 200
	}
	if service == "" {
		return s.logs.All(tail)
	}
	return map[string][]string{service: s.logs.Tail(service, tail)}
}

// Refresh is the snapshot used by the TUI.
func (s *Supervisor) Refresh(context.Context) (state.Runtime, map[string][]string, error) {
	return s.Runtime(), s.Logs("", 400), nil
}

func (s *Supervisor) loop(ctx context.Context) {
	s.mu.Lock()
	for _, svc := range s.plan.Services {
		if s.procs[svc.Name] == nil {
			s.procs[svc.Name] = &procState{
				spec:    svc,
				status:  state.Pending,
				command: svc.Command,
			}
		}
	}
	s.mu.Unlock()
	s.persist()

	pending := map[string]struct{}{}
	for _, svc := range s.plan.Services {
		pending[svc.Name] = struct{}{}
	}
	var seen uint64
	for {
		if ctx.Err() != nil {
			return
		}
		progressed := false
		for _, svc := range s.plan.Services {
			if _, ok := pending[svc.Name]; !ok {
				continue
			}
			if !s.depsReady(svc) {
				continue
			}
			if err := s.startOne(ctx, svc); err != nil {
				s.fail(svc.Name, err)
			}
			delete(pending, svc.Name)
			progressed = true
		}
		if len(pending) == 0 {
			return
		}
		if progressed {
			continue
		}
		var ok bool
		seen, ok = s.wait(ctx, seen)
		if !ok {
			return
		}
	}
}

func (s *Supervisor) depsReady(svc plan.Service) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range svc.DependsOn {
		r := s.procs[name]
		if r == nil {
			return false
		}
		switch r.status {
		case state.Healthy, state.Running:
		default:
			return false
		}
	}
	return true
}

func (s *Supervisor) wait(ctx context.Context, seen uint64) (uint64, bool) {
	s.mu.Lock()
	if s.generation != seen {
		g := s.generation
		s.mu.Unlock()
		return g, true
	}
	ch := s.changed
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return seen, false
	case <-ch:
		s.mu.Lock()
		g := s.generation
		s.mu.Unlock()
		return g, true
	}
}

func (s *Supervisor) notify() {
	s.mu.Lock()
	s.generation++
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

func (s *Supervisor) snapshot() state.Runtime {
	s.mu.Lock()
	defer s.mu.Unlock()
	rt := state.Runtime{
		ID:         state.ProjectID(s.plan.Root),
		Project:    s.plan.Name,
		Root:       s.plan.Root,
		PID:        os.Getpid(),
		Control:    s.controlURL,
		Token:      s.token,
		LogFile:    s.logFile,
		Started:    s.started,
		ShareURL:   s.shareURL,
		ShareHint:  s.shareHint,
		ShareError: s.shareErr,
	}
	for _, svc := range s.plan.Services {
		r := s.procs[svc.Name]
		if r == nil {
			rt.Services = append(rt.Services, state.Service{
				Name: svc.Name, Status: state.Pending, Command: svc.Command,
				Dir: svc.Dir, Protocol: svc.Protocol, Primary: svc.Primary,
			})
			continue
		}
		rt.Services = append(rt.Services, r.view())
	}
	return rt
}

func (p *procState) view() state.Service {
	return state.Service{
		Name:      p.spec.Name,
		Status:    p.status,
		Command:   p.command,
		Dir:       p.spec.Dir,
		PID:       p.pid,
		Port:      p.port,
		URL:       p.url,
		HTTPSURL:  p.httpsURL,
		Primary:   p.spec.Primary,
		Protocol:  p.spec.Protocol,
		LastLine:  p.lastLine,
		Started:   p.started,
		HealthyAt: p.healthy,
	}
}

func (s *Supervisor) persist() {
	_ = state.Save(s.snapshot())
}

func (s *Supervisor) portMap() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.procs))
	for name, r := range s.procs {
		if r.port > 0 {
			out[name] = r.port
		}
	}
	return out
}

func (s *Supervisor) rootCtx() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil {
		return context.Background()
	}
	return s.run
}

func (s *Supervisor) stopAll() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.shutting = true
		var pids []int
		for _, r := range s.procs {
			r.stopping = true
			if r.cancel != nil {
				r.cancel()
			}
			if r.pid > 0 {
				pids = append(pids, r.pid)
			}
		}
		s.mu.Unlock()
		s.notify()
		for _, pid := range pids {
			_ = killPID(pid)
		}
	})
}

func (s *Supervisor) fail(name string, err error) {
	s.mu.Lock()
	r := s.procs[name]
	if r == nil {
		r = &procState{spec: plan.Service{Name: name}}
		s.procs[name] = r
	}
	r.status = state.Exited
	r.lastLine = err.Error()
	snap := r.view()
	hook := s.hooks.OnStatus
	s.mu.Unlock()
	s.addLog(name, err.Error())
	s.notify()
	s.persist()
	if hook != nil {
		hook(snap)
	}
}

func (s *Supervisor) addLog(name, line string) {
	if line == "" {
		return
	}
	s.logs.Add(name, line)
	s.mu.Lock()
	if r := s.procs[name]; r != nil {
		r.lastLine = line
	}
	hook := s.hooks.OnLog
	s.mu.Unlock()
	if hook != nil {
		hook(name, line)
	}
}

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var _ control.Backend = (*Supervisor)(nil)
